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

package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"

	"github.com/gravitational/trace"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mattn/go-shellwords"

	"github.com/gravitational/teleport"
	clientproto "github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/constants"
	"github.com/gravitational/teleport/api/utils/keys"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/auth/authclient"
	"github.com/gravitational/teleport/lib/authz"
	pgmcp "github.com/gravitational/teleport/lib/client/db/mcp/postgres"
	alpncommon "github.com/gravitational/teleport/lib/srv/alpnproxy/common"
	"github.com/gravitational/teleport/lib/utils"
)

type ProxyServerConfig struct {
	Authorizer  authz.Authorizer
	AuthClient  authclient.ClientI
	AccessPoint authclient.ProxyAccessPoint
	ALPNHandler func(ctx context.Context, conn net.Conn) error
}

func (c *ProxyServerConfig) Check() error {
	if c.Authorizer == nil {
		return trace.BadParameter("missing Authorizer")
	}
	if c.AuthClient == nil {
		return trace.BadParameter("missing AuthClient")
	}
	if c.AccessPoint == nil {
		return trace.BadParameter("missing AccessPoint")
	}
	return nil
}

type ProxyServer struct {
	cfg        *ProxyServerConfig
	middleware *auth.Middleware
	logger     *slog.Logger
}

func NewProxyServer(ctx context.Context, cfg *ProxyServerConfig) (*ProxyServer, error) {
	if err := cfg.Check(); err != nil {
		return nil, trace.Wrap(err)
	}

	clusterName, err := cfg.AccessPoint.GetClusterName(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	middleware := &auth.Middleware{
		ClusterName: clusterName.GetClusterName(),
	}

	return &ProxyServer{
		cfg:        cfg,
		middleware: middleware,
		logger:     slog.With(teleport.ComponentKey, teleport.Component(teleport.ComponentProxy, "mcp")),
	}, nil
}

func (s *ProxyServer) HandleConnection(ctx context.Context, conn net.Conn) error {
	defer conn.Close()
	tlsConn, ok := conn.(utils.TLSConn)
	if !ok {
		return trace.BadParameter("expected *tls.Conn, got: %T", conn)
	}

	ctx, err := s.middleware.WrapContextWithUser(ctx, tlsConn)
	if err != nil {
		return trace.Wrap(err)
	}

	authCtx, err := s.cfg.Authorizer.Authorize(ctx)
	if err != nil {
		return trace.Wrap(err)
	}

	if !authCtx.Identity.GetIdentity().RouteToDatabase.Empty() {
		return s.handleOneDB(ctx, conn, authCtx)
	}

	// TODO replace me with real impl
	cmdToRun := os.Getenv("TELEPORT_MCP_RUN_POSTGRES")
	s.logger.DebugContext(ctx, "=== MCP server authorized", "user", authCtx.User, "cmd", cmdToRun)
	if cmdToRun != "" {
		parts, err := shellwords.Parse(cmdToRun)
		if err != nil {
			return trace.BadParameter("cannot parse mcp.run: %v", err)
		}
		s.logger.DebugContext(ctx, "=== running tmp postgres server ", "command", parts)
		cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
		cmd.Stdin = tlsConn
		cmd.Stdout = tlsConn
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			return trace.Wrap(err)
		}
		return cmd.Wait()
	} else {
		_, err := tlsConn.Write([]byte("hello teleport"))
		return trace.Wrap(err)
	}
}

func (s *ProxyServer) handleOneDB(ctx context.Context, clientConn net.Conn, authCtx *authz.Context) error {
	// What the hell am i doing
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return trace.Wrap(err)
	}
	marshal, err := keys.MarshalPrivateKey(ecKey)
	if err != nil {
		return trace.Wrap(err)
	}
	pk, err := keys.ParsePrivateKey(marshal)
	if err != nil {
		return trace.Wrap(err)
	}
	publicKeyPEM, err := keys.MarshalPublicKey(pk.Public())
	if err != nil {
		return trace.Wrap(err, "failed to marshal public key")
	}

	identity := authCtx.Identity.GetIdentity()
	certsReq := clientproto.UserCertsRequest{
		TLSPublicKey:   publicKeyPEM,
		Username:       authCtx.User.GetName(),
		Expires:        identity.Expires,
		Format:         constants.CertificateFormatStandard,
		RouteToCluster: identity.RouteToCluster,
		Usage:          clientproto.UserCertsRequest_Database,
		RouteToDatabase: clientproto.RouteToDatabase{
			ServiceName: identity.RouteToDatabase.ServiceName,
			Username:    identity.RouteToDatabase.Username,
			Database:    identity.RouteToDatabase.Database,
			Protocol:    identity.RouteToDatabase.Protocol,
			Roles:       identity.RouteToDatabase.Roles,
		},
	}

	certs, err := s.cfg.AuthClient.GenerateUserCerts(ctx, certsReq)
	if err != nil {
		return trace.Wrap(err, "failed issuing user certs")
	}

	tlsCert, err := pk.TLSCertificate(certs.TLS)
	if err != nil {
		return trace.Wrap(err)
	}

	alpnProtocol, err := alpncommon.ToALPNProtocol(identity.RouteToDatabase.Protocol)
	if err != nil {
		return trace.Wrap(err)
	}

	tlsConfig := &tls.Config{
		NextProtos:   []string{string(alpnProtocol)},
		Certificates: []tls.Certificate{tlsCert},
		// Use proper server name and root CAs
		InsecureSkipVerify: true,
	}

	utils.SetupTLSConfig(tlsConfig, nil /* let server decide cipher */)

	in, out := net.Pipe()
	defer in.Close()
	defer out.Close()
	go func() {
		if err := s.cfg.ALPNHandler(ctx, out); !utils.IsOKNetworkError(err) {
			s.logger.ErrorContext(ctx, "ALPN handler for database interactive session failed", "error", err)
		}
	}()

	serverConn := tls.Client(in, tlsConfig)
	_, err = serverConn.Write([]byte("whatever"))

	mcpServer := server.NewMCPServer("teleport", teleport.Version)

	// Add PostgreSQL MCP stuff.
	sess, err := pgmcp.NewSession(ctx, pgmcp.NewSessionConfig{
		MCPServer: mcpServer,
		RawDBConn: serverConn,
		Route:     certsReq.RouteToDatabase,
	})
	defer sess.Close(ctx)

	err = server.NewStdioServer(mcpServer).Listen(ctx, clientConn, clientConn)
	s.logger.DebugContext(ctx, "MCP session terminated", "error", err)
	return trace.Wrap(err)
}
