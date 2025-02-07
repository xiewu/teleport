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
	"bytes"
	"fmt"
	"io"
	"iter"
	"net"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/gravitational/trace"
	"github.com/mattn/go-shellwords"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/tlsca"
	"github.com/gravitational/teleport/lib/utils"
	logutils "github.com/gravitational/teleport/lib/utils/log"
	"github.com/gravitational/teleport/tool/common"
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

	logger.DebugContext(cf.Context, "Fetched databases.", "database_services", logutils.IterCollectorAttr(resourceNameIterator(databases)))
	if len(databases) == 0 {
		return trace.BadParameter("no databases found")
	}

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
		lp, err := startDatabaseLocalProxy(cf.Context, cf, tc, profile, dbInfo, requires)
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
		switch cf.DryRun {
		case true:
			fmt.Fprintf(cf.Stdout(), "Execute command for database service %s: %s\n", db.GetName(), dbCmd)
		default:
			dbCmd.Stdout = newDatabaseExecOutputWriter(cf, db)
			dbCmd.Stderr = newDatabaseExecErrorWriter(cf, db)
			if err := cf.RunCommand(dbCmd); err != nil {
				errMsg := fmt.Sprintf("Failed to execute database service %s: %v.", db.GetName(), err)
				dbCmd.Stderr.Write([]byte(errMsg))
			}
		}
	}
	return nil
}

func (c *databaseExecCommand) checkInputs(cf *CLIConf) error {
	// TODO(greedy52) support selecting individual databases
	switch {
	case cf.Labels == "" && cf.PredicateExpression == "":
		return trace.BadParameter("At least one of --labels,--query must be specified")
	}

	// TODO(greedy52) support command template:
	switch {
	case cf.DatabaseQuery == "":
		return trace.BadParameter("--exec-query must be specified")
	}
	return nil
}

func (c *databaseExecCommand) fetchDatabases(cf *CLIConf, tc *client.TeleportClient) ([]types.Database, error) {
	// TODO(greedy52) if len(cf.DatabaseServices) > 0
	dbs, err := tc.ListDatabases(cf.Context, tc.ResourceFilter(types.KindDatabaseServer))
	if err != nil {
		return nil, trace.Wrap(err)
	}

	// Pre-checks.
	for _, db := range dbs {
		if isDatabaseUserRequired(db.GetProtocol()) && cf.DatabaseUser == "" {
			return nil, trace.BadParameter("--db-user is required for database %s", db.GetName())
		}
		if isDatabaseNameRequired(db.GetProtocol()) && cf.DatabaseName == "" {
			return nil, trace.BadParameter("--db-name is required for database %s", db.GetName())
		}
	}
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

type ioWriterFunc func([]byte) (n int, err error)

func (f ioWriterFunc) Write(p []byte) (n int, err error) {
	return f(p)
}

func nonEmptyLines(input string) []string {
	var lines []string
	for _, line := range strings.Split(input, "\n") {
		trimmed := strings.TrimSpace(line) // Remove extra spaces
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

type databaseExecWriter struct {
	io.Writer
	infoType      string
	dbServiceName string
	enableColor   bool
	color         string
}

func (w *databaseExecWriter) Write(p []byte) (n int, err error) {
	for _, line := range strings.Split(string(p), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		msg := fmt.Sprintf("[%v][%s][%s]: ", time.Now().Format(time.RFC3339), w.dbServiceName, w.infoType)
		if w.enableColor {
			msg = w.color + msg + "\033[0m"
		}
		msg += trimmed
		fmt.Fprintln(w.Writer, msg)
	}
	return len(p), nil
}

func newDatabaseExecOutputWriter(cf *CLIConf, db types.Database) io.Writer {
	return &databaseExecWriter{
		Writer:        cf.Stdout(),
		infoType:      "output",
		dbServiceName: common.FormatResourceName(db, false),
		enableColor:   utils.IsTerminal(os.Stdout),
		color:         "\033[32m", // Green
	}
}

func newDatabaseExecErrorWriter(cf *CLIConf, db types.Database) io.Writer {
	return &databaseExecWriter{
		Writer:        cf.Stderr(),
		infoType:      "error!",
		dbServiceName: common.FormatResourceName(db, false),
		enableColor:   utils.IsTerminal(os.Stderr),
		color:         "\033[33m", // Yellow
	}
}
