// Teleport
// Copyright (C) 2024 Gravitational, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package vnet

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"

	"github.com/gravitational/teleport"
	proxyclient "github.com/gravitational/teleport/api/client/proxy"
	tracessh "github.com/gravitational/teleport/api/observability/tracing/ssh"
	"github.com/gravitational/teleport/api/utils/grpc/interceptors"
	"github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
)

type sshProvider interface {
	ResolveSSHInfo(ctx context.Context, fqdn string) (*SSHInfo, error)
	TeleportClientTLSConfig(ctx context.Context, profileName, clusterName string) (*tls.Config, error)
	UserSSHConfig(ctx context.Context, sshInfo *SSHInfo, username string) (*ssh.ClientConfig, error)
}

type sshResolver struct {
	sshProvider sshProvider
	log         *slog.Logger
	clock       clockwork.Clock
}

func newSSHResolver(sshProvider sshProvider, clock clockwork.Clock) *sshResolver {
	return &sshResolver{
		sshProvider: sshProvider,
		log:         log.With(teleport.ComponentKey, "VNet.SSHResolver"),
		clock:       clock,
	}
}

func (r sshResolver) resolveTCPHandler(ctx context.Context, fqdn string) (*tcpHandlerSpec, error) {
	sshInfo, err := r.sshProvider.ResolveSSHInfo(ctx, fqdn)
	if err != nil {
		return nil, err
	}
	sshHandler := r.newSSHHandler(ctx, sshInfo)
	return &tcpHandlerSpec{
		ipv4CIDRRange: sshInfo.Ipv4CidrRange,
		tcpHandler:    sshHandler,
	}, nil
}

func (r *sshResolver) newSSHHandler(ctx context.Context, sshInfo *SSHInfo) *sshHandler {
	return &sshHandler{
		sshInfo:     sshInfo,
		sshProvider: r.sshProvider,
	}
}

type sshHandler struct {
	sshInfo     *SSHInfo
	sshProvider sshProvider
}

func (h *sshHandler) handleTCPConnector(ctx context.Context, localPort uint16, connector func() (net.Conn, error)) error {
	targetTCPConn, err := h.dialTargetTCP(ctx)
	if err != nil {
		return trace.Wrap(err, "dialing SSH host %s", h.sshInfo.Hostname)
	}
	_, _, err = h.dialTargetSSH(ctx, targetTCPConn, "nic")
	if err != nil {
		return trace.Wrap(err, "initiating SSH connection to target")
	}
	localTCPConn, err := connector()
	if err != nil {
		return trace.Wrap(err, "unwrapping local VNet TCP conn")
	}
	return trace.Wrap(utils.ProxyConn(ctx, localTCPConn, targetTCPConn))
}

func (h *sshHandler) dialTargetSSH(ctx context.Context, tcpConn net.Conn, username string) (any, any, error) {
	sshClientConfig, err := h.sshProvider.UserSSHConfig(ctx, h.sshInfo, username)
	if err != nil {
		return nil, nil, trace.Wrap(err, "getting user SSH client config")
	}
	sshconn, chans, reqs, err := tracessh.NewClientConn(ctx, tcpConn, h.sshInfo.Addr, sshClientConfig)
	if err != nil {
		return nil, nil, trace.Wrap(err, "dialing target SSH node")
	}
	log.DebugContext(ctx, "Dialed target SSH node",
		"sshconn", sshconn,
		"chans", chans,
		"reqs", reqs,
	)
	return nil, nil, trace.NotImplemented("dialTargetSSH is not implemented")
}

func (h *sshHandler) dialTargetTCP(ctx context.Context) (net.Conn, error) {
	proxyClientConfig := proxyclient.ClientConfig{
		ProxyAddress:      h.sshInfo.DialOptions.WebProxyAddr,
		TLSRoutingEnabled: true,
		TLSConfigFunc: func(cluster string) (*tls.Config, error) {
			return h.sshProvider.TeleportClientTLSConfig(ctx, h.sshInfo.Profile, cluster)
		},
		UnaryInterceptors:  []grpc.UnaryClientInterceptor{interceptors.GRPCClientUnaryErrorInterceptor},
		StreamInterceptors: []grpc.StreamClientInterceptor{interceptors.GRPCClientStreamErrorInterceptor},
		// This empty SSH client config should never be used, we dial to the
		// proxy over TLS.
		SSHConfig:               &ssh.ClientConfig{},
		InsecureSkipVerify:      h.sshInfo.DialOptions.InsecureSkipVerify,
		ALPNConnUpgradeRequired: h.sshInfo.DialOptions.AlpnConnUpgradeRequired,
	}
	pclt, err := proxyclient.NewClient(ctx, proxyClientConfig)
	if err != nil {
		return nil, trace.Wrap(err, "creating proxy client")
	}
	log.DebugContext(ctx, "Dialing target host",
		"target", h.sshInfo.Addr,
	)
	targetConn, _, err := pclt.DialHost(ctx, h.sshInfo.Addr, h.sshInfo.Cluster, nil /*keyRing*/)
	return targetConn, trace.Wrap(err)
}
