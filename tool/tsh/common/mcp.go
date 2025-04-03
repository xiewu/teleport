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
	"context"
	"io"
	"net"
	"os"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/client/mcp"
	"github.com/gravitational/teleport/lib/srv/alpnproxy"
	alpncommon "github.com/gravitational/teleport/lib/srv/alpnproxy/common"
	"github.com/gravitational/teleport/lib/utils"
	listenerutils "github.com/gravitational/teleport/lib/utils/listener"
)

func onMCPStart(cf *CLIConf) error {
	cf.OverrideStdout = io.Discard

	err := onAppLogin(cf)
	if err != nil {
		return trace.Wrap(err)
	}

	tc, err := makeClient(cf)
	if err != nil {
		return trace.Wrap(err)
	}

	cert, err := loadAppCertificate(tc, cf.AppName)
	if err != nil {
		return trace.Wrap(err)
	}

	in, out := net.Pipe()
	listener := listenerutils.NewSingleUseListener(out)
	defer listener.Close()

	lp, err := alpnproxy.NewLocalProxy(
		makeBasicLocalProxyConfig(cf.Context, tc, listener, tc.InsecureSkipVerify),
		alpnproxy.WithALPNProtocol(alpncommon.ProtocolTCP),
		alpnproxy.WithClientCert(cert),
		alpnproxy.WithClusterCAsIfConnUpgrade(cf.Context, tc.RootClusterCACertPool),
	)
	if err != nil {
		return trace.Wrap(err)
	}
	go func() {
		defer lp.Close()
		if err = lp.Start(cf.Context); err != nil {
			logger.ErrorContext(cf.Context, "Failed to start local ALPN proxy", "error", err)
		}
	}()

	stdioConn := utils.CombinedStdio{}
	return utils.ProxyConn(cf.Context, in, stdioConn)
}

func onMCPForward(cf *CLIConf) error {
	cf.OverrideStdout = io.Discard

	tc, err := makeClient(cf)
	if err != nil {
		return trace.Wrap(err)
	}
	clusterClient, err := tc.ConnectToCluster(cf.Context)
	if err != nil {
		return trace.Wrap(err)
	}
	defer clusterClient.Close()

	dialAppServer := func(ctx context.Context, appServer types.AppServer) (io.ReadCloser, io.WriteCloser, error) {
		cf := *cf
		tc, err := makeClient(&cf)
		if err != nil {
			return nil, nil, trace.Wrap(err)
		}
		cf.AppName = appServer.GetApp().GetName()
		if err := onAppLogin(&cf); err != nil {
			return nil, nil, trace.Wrap(err)
		}
		cert, err := loadAppCertificate(tc, cf.AppName)
		if err != nil {
			return nil, nil, trace.Wrap(err)
		}
		left, right := net.Pipe()
		listener := listenerutils.NewSingleUseListener(right)
		lp, err := alpnproxy.NewLocalProxy(
			makeBasicLocalProxyConfig(cf.Context, tc, listener, tc.InsecureSkipVerify),
			alpnproxy.WithALPNProtocol(alpncommon.ProtocolTCP),
			alpnproxy.WithClientCert(cert),
			alpnproxy.WithClusterCAsIfConnUpgrade(cf.Context, tc.RootClusterCACertPool),
		)
		if err != nil {
			return nil, nil, trace.Wrap(err)
		}
		go func() {
			defer logger.InfoContext(ctx, "Local proxy for MCP app exited", "app", appServer.GetName())
			logger.InfoContext(ctx, "Starting local proxy for MCP app", "app", appServer.GetName())
			if err = lp.Start(ctx); err != nil {
				logger.ErrorContext(ctx, "Failed to start local ALPN proxy", "error", err)
			}
		}()
		return left, left, nil
	}

	proxy, err := mcp.NewProxy(cf.Context, mcp.ProxyConfig{
		AppDialerFn:      dialAppServer,
		Events:           clusterClient.AuthClient,
		AppServersGetter: clusterClient.AuthClient,
	})
	if err != nil {
		logger.ErrorContext(cf.Context, "Failed to start MCP proxy",
			"error", err,
		)
		return trace.Wrap(err)
	}

	return trace.Wrap(
		proxy.Listen(cf.Context, os.Stdin, os.Stdout),
	)
}
