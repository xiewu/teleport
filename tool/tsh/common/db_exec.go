/*
 * Teleport
 * Copyright (C) 2025  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package common

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"iter"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	"github.com/gravitational/trace"
	"github.com/mattn/go-shellwords"

	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/mfa"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/srv/alpnproxy"
	"github.com/gravitational/teleport/lib/tlsca"
	"github.com/gravitational/teleport/lib/utils"
	logutils "github.com/gravitational/teleport/lib/utils/log"
)

func resourceNameIterator[T types.Resource](s []T) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, t := range s {
			if !yield(types.GetName(t)) {
				return
			}
		}
	}
}

type databaseExecCommand struct {
	//TODO(greedy52) consider moving some states here instead of passing around
	//as function params.

	clusterClient      *client.ClusterClient
	reuseMFAResponse   *proto.MFAAuthenticateResponse
	reuseMFAResponseMu sync.Mutex
}

func (c *databaseExecCommand) run(cf *CLIConf) error {
	if err := c.checkInputs(cf); err != nil {
		return err
	}

	tc, err := makeClient(cf)
	if err != nil {
		return trace.Wrap(err)
	}
	profile, err := tc.ProfileStatus()
	if err != nil {
		return trace.Wrap(err)
	}

	databases, err := c.fetchDatabases(cf, tc)
	if err != nil {
		return trace.Wrap(err)
	}

	logger.DebugContext(cf.Context, "Fetched databases.", "database_services", logutils.CollectorAttr(resourceNameIterator(databases)))
	if len(databases) == 0 {
		return trace.BadParameter("no databases found")
	}

	clusterClient, err := tc.ConnectToCluster(cf.Context)
	if err != nil {
		return trace.Wrap(err)
	}
	defer clusterClient.Close()
	c.clusterClient = clusterClient

	ctx := context.WithValue(cf.Context, "db-exec-mfa", c.reuseMFA)

	// TODO(greedy52) run parallel with errgroup
	for _, db := range databases {
		dbInfo := &databaseInfo{
			RouteToDatabase: tlsca.RouteToDatabase{
				ServiceName: db.GetName(),
				Protocol:    db.GetProtocol(),
				Username:    cf.DatabaseUser,
				Database:    cf.DatabaseName,
				Roles:       requestedDatabaseRoles(cf),
			},
			database: db,
		}

		requires := &dbLocalProxyRequirement{
			localProxy: true,
			tunnel:     true,
		}
		lp, err := c.startLocalProxy(ctx, cf, tc, profile, dbInfo, requires)
		if err != nil {
			return trace.Wrap(err)
		}

		dbCmd, err := c.makeCommand(cf, tc, dbInfo, lp.GetAddr())
		if err != nil {
			return trace.Wrap(err)
		}

		logger.DebugContext(cf.Context, "Executing database command", "command", dbCmd)

		// TODO(greedy52) add some line prefix to differentiate output from the
		// targets.
		var logFileName string
		if cf.SSHLogDir != "" {
			logFileName = filepath.Join(cf.SSHLogDir, dbInfo.ServiceName+".log")
			fmt.Fprintf(cf.Stdout(), "Execute command for database service %s. Logs will be saved at %q.\n", db.GetName(), logFileName)
		} else {
			fmt.Fprintf(cf.Stdout(), "Execute command for database service %s.\n", db.GetName())
		}
		if !cf.DryRun {
			if logFileName != "" {
				logFilePath, err := utils.EnsureLocalPath(logFileName, "", "")
				if err != nil {
					return trace.Wrap(err)
				}
				logFile, err := os.Create(logFilePath)
				if err != nil {
					return trace.Wrap(err)
				}
				dbCmd.Stdout = logFile
				dbCmd.Stderr = logFile
			} else {
				dbCmd.Stdout = cf.Stdout()
				dbCmd.Stderr = cf.Stderr()
			}
			if err := cf.RunCommand(dbCmd); err != nil {
				errMsg := fmt.Sprintf("Failed to execute database service %s: %v.\n", db.GetName(), err)
				dbCmd.Stderr.Write([]byte(errMsg))
			}
		}
		fmt.Fprintln(cf.Stdout(), "")
	}
	return nil
}

func (c *databaseExecCommand) checkInputs(cf *CLIConf) error {
	// TODO(greedy52) support selecting individual databases
	switch {
	case cf.Labels == "" && cf.PredicateExpression == "" && cf.SearchKeywords == "" && len(cf.DatabaseServices) == 0:
		return trace.BadParameter("Provide at least one database service names or use one of --search-labels,--search-keywords,--search-query")
	}

	// TODO(greedy52) support command template
	switch {
	case cf.DatabaseQuery == "":
		return trace.BadParameter("--exec-query must be specified")
	}
	return nil
}

func (c *databaseExecCommand) fetchDatabases(cf *CLIConf, tc *client.TeleportClient) (dbs []types.Database, err error) {
	if len(cf.DatabaseServices) == 0 {
		return c.searchDatabases(cf, tc)
	}

	for _, service := range cf.DatabaseServices {
		database, err := getDatabase(cf.Context, tc, service)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		dbs = append(dbs, database)
	}
	if err := c.precheckDatabases(cf, dbs); err != nil {
		return nil, trace.Wrap(err)
	}
	return dbs, nil
}

func (c *databaseExecCommand) precheckDatabases(cf *CLIConf, dbs []types.Database) error {
	// Pre-checks.
	for _, db := range dbs {
		if isDatabaseUserRequired(db.GetProtocol()) && cf.DatabaseUser == "" {
			return trace.BadParameter("--db-user is required for database %s", db.GetName())
		}
		if isDatabaseNameRequired(db.GetProtocol()) && cf.DatabaseName == "" {
			return trace.BadParameter("--db-name is required for database %s", db.GetName())
		}
	}
	return nil
}

func (c *databaseExecCommand) searchDatabases(cf *CLIConf, tc *client.TeleportClient) ([]types.Database, error) {
	dbs, err := tc.ListDatabases(cf.Context, tc.ResourceFilter(types.KindDatabaseServer))
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if err := c.precheckDatabases(cf, dbs); err != nil {
		return nil, trace.Wrap(err)
	}

	// prompt
	fmt.Fprintf(cf.Stdout(), "Found %d databases:\n\n", len(dbs))

	var rows []databaseTableRow
	for _, db := range dbs {
		rows = append(rows, getDatabaseRow(
			"",
			"",
			cf.SiteName,
			db,
			nil,
			nil,
			cf.Verbose))
	}
	printDatabaseTable(printDatabaseTableConfig{
		writer:         cf.Stdout(),
		rows:           rows,
		includeColumns: []string{"Name", "Protocol", "Description", "Labels"},
	})

	fmt.Fprintln(cf.Stdout(), "Tip: use --skip-confirm to skip this confirmation.")
	fmt.Fprint(cf.Stdout(), "Do you want to continue? (Press <enter> to proceed or Ctrl+C/Command+C to exit): ")
	reader := bufio.NewReader(cf.Stdin())
	_, _ = reader.ReadString('\n')
	fmt.Fprintln(cf.Stdout(), "")
	return dbs, nil
}

func (c *databaseExecCommand) makeCommand(cf *CLIConf, tc *client.TeleportClient, dbInfo *databaseInfo, lpAddr string) (*exec.Cmd, error) {
	host, port, err := net.SplitHostPort(lpAddr)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// TODO(greedy52) do this properly in other places and support general
	// command template. This is just an example to make mysql works.
	templ, err := template.New("dbcmd").Parse(
		`mysql --user {{.db_user}} --port {{.db_port}} --host {{.db_host}} --protocol TCP -e "{{.db_query}}"`,
	)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	var cmdBuffer bytes.Buffer
	err = templ.Execute(&cmdBuffer, map[string]any{
		"db_host":     host,
		"db_port":     port,
		"db_query":    cf.DatabaseQuery,
		"db_user":     dbInfo.Username,
		"db_name":     dbInfo.Database,
		"db_service":  dbInfo.ServiceName,
		"db_protocol": dbInfo.Protocol,
		"db_roles":    dbInfo.Roles,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	words, err := shellwords.Parse(cmdBuffer.String())
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if len(words) == 0 {
		return nil, trace.BadParameter("query is empty")
	}

	return exec.CommandContext(cf.Context, words[0], words[1:]...), nil
}

func (c *databaseExecCommand) reuseMFA(ctx context.Context) (*proto.MFAAuthenticateResponse, error) {
	c.reuseMFAResponseMu.Lock()
	defer c.reuseMFAResponseMu.Unlock()
	if c.reuseMFAResponse != nil {
		return c.reuseMFAResponse, nil
	}
	response, err := mfa.PerformDBExecMFACeremony(ctx, c.clusterClient.AuthClient.PerformMFACeremony)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	c.reuseMFAResponse = response
	return c.reuseMFAResponse, nil
}

func (c *databaseExecCommand) startLocalProxy(ctx context.Context, cf *CLIConf,
	tc *client.TeleportClient, profile *client.ProfileStatus,
	dbInfo *databaseInfo, requires *dbLocalProxyRequirement,
) (*alpnproxy.LocalProxy, error) {
	listener, err := createLocalProxyListener("localhost:0", dbInfo.RouteToDatabase, profile)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	opts := []alpnproxy.LocalProxyConfigOpt{
		alpnproxy.WithDatabaseProtocol(dbInfo.Protocol),
		alpnproxy.WithClusterCAsIfConnUpgrade(cf.Context, tc.RootClusterCACertPool),
	}
	cc := client.NewDBCertChecker(tc, dbInfo.RouteToDatabase, nil, client.WithTTL(time.Duration(cf.MinsToLive)*time.Minute))
	opts = append(opts, alpnproxy.WithMiddleware(cc))

	lp, err := alpnproxy.NewLocalProxy(makeBasicLocalProxyConfig(cf.Context, tc, listener, cf.InsecureSkipVerify), opts...)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// Force a retrieval before local proxy start
	if err := cc.OnNewConnection(ctx, lp); err != nil {
		return nil, trace.Wrap(err)
	}
	go func() {
		defer listener.Close()
		if err := lp.Start(ctx); err != nil {
			logger.ErrorContext(cf.Context, "Failed to start local proxy", "error", err)
		}
	}()
	return lp, nil
}
