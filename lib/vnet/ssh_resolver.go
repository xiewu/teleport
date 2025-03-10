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
	"sync/atomic"

	"github.com/gravitational/teleport"
	proxyclient "github.com/gravitational/teleport/api/client/proxy"
	tracessh "github.com/gravitational/teleport/api/observability/tracing/ssh"
	"github.com/gravitational/teleport/api/utils/grpc/interceptors"
	"github.com/gravitational/teleport/lib/cryptosuites"
	"github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
)

type sshProvider interface {
	ResolveSSHInfo(ctx context.Context, fqdn string) (*SSHInfo, error)
	TeleportClientTLSConfig(ctx context.Context, profileName, clusterName string) (*tls.Config, error)
	UserSSHConfig(ctx context.Context, sshInfo *SSHInfo, username string) (*ssh.ClientConfig, error)
}

type sshResolver struct {
	sshProvider     sshProvider
	log             *slog.Logger
	clock           clockwork.Clock
	sshServerConfig *ssh.ServerConfig
}

func newSSHResolver(sshProvider sshProvider, clock clockwork.Clock) *sshResolver {
	hostKey, err := cryptosuites.GenerateKeyWithAlgorithm(cryptosuites.Ed25519)
	if err != nil {
		panic(err)
	}
	hostSigner, err := ssh.NewSignerFromSigner(hostKey)
	if err != nil {
		panic(err)
	}
	sshServerConfig := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	sshServerConfig.AddHostKey(hostSigner)
	return &sshResolver{
		sshProvider:     sshProvider,
		log:             log.With(teleport.ComponentKey, "VNet.SSHResolver"),
		clock:           clock,
		sshServerConfig: sshServerConfig,
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
		sshInfo:         sshInfo,
		sshProvider:     r.sshProvider,
		sshServerConfig: r.sshServerConfig,
	}
}

type sshHandler struct {
	sshInfo         *SSHInfo
	sshProvider     sshProvider
	sshServerConfig *ssh.ServerConfig

	fg              singleflight.Group
	sshClientConfig atomic.Pointer[ssh.ClientConfig]
}

func (h *sshHandler) handleTCPConnector(ctx context.Context, localPort uint16, connector func() (net.Conn, error)) error {
	targetTCPConn, err := h.dialTargetTCP(ctx)
	if err != nil {
		return trace.Wrap(err, "dialing SSH host %s", h.sshInfo.Hostname)
	}
	defer targetTCPConn.Close()

	localTCPConn, err := connector()
	if err != nil {
		return trace.Wrap(err, "unwrapping local VNet TCP conn")
	}
	defer localTCPConn.Close()

	serverConn, chans, requests, err := ssh.NewServerConn(localTCPConn, h.sshServerConfig)
	if err != nil {
		return trace.Wrap(err, "accepting incoming SSH conn")
	}
	defer serverConn.Close()

	sshClient, err := h.dialTargetSSH(ctx, targetTCPConn, serverConn.User())
	if err != nil {
		return trace.Wrap(err, "initiating SSH connection to target")
	}
	defer sshClient.Close()

	return trace.Wrap(forwardSSHConnection(ctx, sshClient, serverConn, chans, requests), "proxying SSH connection")
}

func (h *sshHandler) dialTargetTCP(ctx context.Context) (net.Conn, error) {
	proxyClientConfig := proxyclient.ClientConfig{
		ProxyAddress: h.sshInfo.DialOptions.WebProxyAddr,
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

func (h *sshHandler) dialTargetSSH(ctx context.Context, tcpConn net.Conn, username string) (*ssh.Client, error) {
	sshClientConfig, err := h.userSSHConfig(ctx, username)
	if err != nil {
		return nil, trace.Wrap(err, "getting user SSH client config")
	}
	sshconn, chans, reqs, err := tracessh.NewClientConn(ctx, tcpConn, h.sshInfo.Addr, sshClientConfig)
	if err != nil {
		log.InfoContext(ctx, "Error dialing target SSH node, retrying with a fresh user cert", "error", err)
		sshClient, err := h.retryDialTargetSSH(ctx, username)
		return sshClient, trace.Wrap(err)
	}
	log.DebugContext(ctx, "Dialed target SSH node", "target", h.sshInfo.Addr)
	return ssh.NewClient(sshconn, chans, reqs), nil
}

func (h *sshHandler) retryDialTargetSSH(ctx context.Context, username string) (*ssh.Client, error) {
	h.sshClientConfig.Store(nil)
	sshClientConfig, err := h.userSSHConfig(ctx, username)
	if err != nil {
		return nil, trace.Wrap(err, "getting fresh SSH client config")
	}
	// We need a fresh TCP connection to the target.
	tcpConn, err := h.dialTargetTCP(ctx)
	if err != nil {
		return nil, trace.Wrap(err, "redialing target with fresh SSH cert")
	}
	sshconn, chans, reqs, err := tracessh.NewClientConn(ctx, tcpConn, h.sshInfo.Addr, sshClientConfig)
	if err != nil {
		return nil, trace.Wrap(err, "dialing target SSH node with fresh user cert")
	}
	return ssh.NewClient(sshconn, chans, reqs), nil
}

func (h *sshHandler) userSSHConfig(ctx context.Context, username string) (*ssh.ClientConfig, error) {
	if c := h.sshClientConfig.Load(); c != nil {
		return c, nil
	}
	_, err, _ := h.fg.Do("", func() (any, error) {
		if c := h.sshClientConfig.Load(); c != nil {
			return nil, nil
		}
		c, err := h.sshProvider.UserSSHConfig(ctx, h.sshInfo, username)
		if err != nil {
			return nil, trace.Wrap(err, "getting user SSH client config")
		}
		h.sshClientConfig.Store(c)
		return nil, nil
	})
	return h.sshClientConfig.Load(), trace.Wrap(err)
}

// forwardSSHConnection forwards all SSH traffic—both global requests and channels—
// from serverConn to targetClient, and vice versa.
func forwardSSHConnection(
	ctx context.Context,
	targetClient *ssh.Client,
	serverConn *ssh.ServerConn,
	channels <-chan ssh.NewChannel,
	requests <-chan *ssh.Request,
) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return forwardGlobalRequests(ctx, targetClient, requests)
	})
	g.Go(func() error {
		return forwardChannels(ctx, g, targetClient, channels)
	})
	return trace.Wrap(g.Wait(), "forwarding SSH connection")
}

func forwardGlobalRequests(
	ctx context.Context,
	targetClient *ssh.Client,
	requests <-chan *ssh.Request,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req, ok := <-requests:
			if !ok {
				return nil
			}
			ok, reply, err := targetClient.SendRequest(req.Type, req.WantReply, req.Payload)
			if err != nil {
				err = trace.Wrap(err, "forwarding global request to target")
				if replyErr := req.Reply(false, nil); replyErr != nil {
					return trace.NewAggregate(err, trace.Wrap(replyErr, "replying to request with error"))
				}
				return err
			}
			if err := req.Reply(ok, reply); err != nil {
				return trace.Wrap(err, "forwarding reply from target back to client")
			}
		}
	}
}

func forwardChannels(
	ctx context.Context,
	g *errgroup.Group,
	targetClient *ssh.Client,
	channels <-chan ssh.NewChannel,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case newChan, ok := <-channels:
			if !ok {
				return nil
			}
			// Open a corresponding channel to the target.
			targetChan, targetRequests, err := targetClient.OpenChannel(newChan.ChannelType(), newChan.ExtraData())
			if err != nil {
				err = trace.Wrap(err, "failed to open channel on target")
				if rejectErr := newChan.Reject(ssh.ConnectionFailed, err.Error()); rejectErr != nil {
					return trace.NewAggregate(err, rejectErr)
				}
				return err
			}
			// Accept the incoming channel request.
			serverChan, serverRequests, err := newChan.Accept()
			if err != nil {
				targetChan.Close()
				return trace.Wrap(err, "accepting incoming channel request")
			}
			forwardChannel(ctx, g,
				serverChan, serverRequests,
				targetChan, targetRequests,
			)
		}
	}
}

func forwardChannel(
	ctx context.Context,
	g *errgroup.Group,
	serverChan ssh.Channel, serverRequests <-chan *ssh.Request,
	targetChan ssh.Channel, targetRequests <-chan *ssh.Request,
) {
	g.Go(func() error {
		// This will close serverChan and targetChan before returning.
		if err := utils.ProxyConn(ctx, serverChan, targetChan); err != nil {
			log.InfoContext(ctx, "Proxying SSH channel failed",
				"error", err)
		}
		return nil
	})
	g.Go(func() error {
		if err := forwardChannelRequests(ctx, targetChan, serverChan, serverRequests); err != nil {
			log.InfoContext(ctx, "Forwarding channel requests from server to target failed",
				"error", err)
		}
		return nil
	})
	g.Go(func() error {
		if err := forwardChannelRequests(ctx, serverChan, targetChan, targetRequests); err != nil {
			log.InfoContext(ctx, "Forwarding channel requests from target to server failed",
				"error", err)
		}
		return nil
	})
}

func forwardChannelRequests(
	ctx context.Context,
	dst, src ssh.Channel,
	requests <-chan *ssh.Request,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req, ok := <-requests:
			if !ok {
				return nil
			}
			ok, err := dst.SendRequest(req.Type, req.WantReply, req.Payload)
			if err != nil {
				err = trace.Wrap(err, "forwarding channel request")
				if replyErr := req.Reply(false, nil); replyErr != nil {
					return trace.NewAggregate(err, replyErr)
				}
				return err
			}
			if err := req.Reply(ok, nil); err != nil {
				return trace.Wrap(err, "forwarding reply to channel request")
			}
		}
	}
}
