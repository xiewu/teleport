package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"strings"
	"sync"

	"github.com/gravitational/trace"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/services"
)

// AppDialerFunc dials an MCP application server and returns two ends of a pipe
// that can be used to read/write bytes to the app server.
type AppDialerFunc func(ctx context.Context, appServer types.AppServer) (io.ReadCloser, io.WriteCloser, error)

type ProxyConfig struct {
	// AppDialerFn is a callback that abstracts away dialing an upstream MCP app server
	AppDialerFn AppDialerFunc
	// Events is used to watch app servers
	Events           types.Events
	AppServersGetter services.AppServersGetter
}

func (c *ProxyConfig) check() error {
	if c.AppDialerFn == nil {
		return trace.BadParameter("missing app dialer")
	}
	if c.Events == nil {
		return trace.BadParameter("missing events client")
	}
	if c.AppServersGetter == nil {
		return trace.BadParameter("missing app servers getter")
	}
	return nil
}

type MCPProxy interface {
	Listen(ctx context.Context, stdin io.Reader, stdout io.Writer) error
	Close() error
}

func NewProxy(ctx context.Context, cfg ProxyConfig) (MCPProxy, error) {
	if err := cfg.check(); err != nil {
		return nil, trace.Wrap(err)
	}
	srv := server.NewMCPServer(
		"teleport/proxy",
		teleport.Version,
		server.WithToolCapabilities(true),
	)
	p := &proxy{
		cfg:        cfg,
		log:        slog.With(teleport.ComponentKey, teleport.ComponentMCP),
		server:     srv,
		appServers: make(map[string]types.AppServer),
		clients:    make(map[string]mcpclient.MCPClient),
	}
	if err := p.start(ctx); err != nil {
		return nil, trace.Wrap(err)
	}
	return p, nil
}

type proxy struct {
	cfg ProxyConfig
	log *slog.Logger

	mu         sync.RWMutex
	appServers map[string]types.AppServer
	clients    map[string]mcpclient.MCPClient
	server     *server.MCPServer
}

func (p *proxy) Listen(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	err := server.NewStdioServer(p.server).Listen(ctx, stdin, stdout)
	if err != nil {
		return trace.Wrap(err, "error from MCP proxy listener")
	}
	return nil
}

func (p *proxy) Close() error {
	var errors []error
	for name, c := range p.clients {
		if err := c.Close(); err != nil {
			err = trace.Wrap(err, "failed to close upstream client %v", name)
			errors = append(errors, err)
		}
	}
	return trace.NewAggregate(errors...)
}

func (p *proxy) start(ctx context.Context) error {
	p.log.InfoContext(ctx, "Starting MCP forward proxy")
	if err := p.watchAppServers(ctx); err != nil {
		return trace.Wrap(err, "failed to watch app servers")
	}
	return nil
}

// Starts the proxy. The proxy will watch for tools to advertise to clients
// with re-exported tool names.
func (p *proxy) watchAppServers(ctx context.Context) error {
	watcher, err := services.NewAppServerWatcher(ctx, services.AppServerWatcherConfig{
		Reader: p.cfg.AppServersGetter,
		ResourceWatcherConfig: services.ResourceWatcherConfig{
			Component: teleport.ComponentMCP,
			Logger:    p.log,
			Client:    p.cfg.Events,
		},
	})
	if err != nil {
		return trace.Wrap(err, "failed to create app server watcher")
	}

	var newResources []types.AppServer
	reconciler, err := services.NewReconciler(services.ReconcilerConfig[types.AppServer]{
		Matcher: func(as types.AppServer) bool {
			return as.GetApp().IsMCP()
		},
		GetCurrentResources: p.getCurrentResources,
		CompareResources: func(a types.AppServer, b types.AppServer) int {
			res := services.CompareServers(a, b)
			p.log.Info("Compared upstream app servers",
				"upstream", a.GetApp().GetName(),
				"result", res,
			)
			switch res {
			case services.Equal, services.Different:
				return res
			case services.OnlyTimestampsDifferent:
				// ignore timestamp changes
				return services.Equal
			default:
				return res
			}
		},
		GetNewResources: func() map[string]types.AppServer {
			out := map[string]types.AppServer{}
			for _, r := range newResources {
				out[r.GetApp().GetName()] = r
			}
			return out
		},
		OnCreate: p.registerAppServer,
		OnUpdate: p.updateAppServer,
		OnDelete: p.removeAppServer,
		Logger:   p.log.With("kind", types.KindAppServer),
	})
	if err != nil {
		return trace.Wrap(err, "failed to create reconciler")
	}
	// wait for init
	newResources = <-watcher.ResourcesC
	// not all clients supported tool list changes (evidently claude does not?)
	// therefore we block to init the server with all tools we know of
	// before returning
	appNames := make([]string, 0, len(newResources))
	for _, r := range newResources {
		appNames = append(appNames, r.GetName())
	}
	p.log.InfoContext(ctx, "Received updated list of app servers from watcher",
		"apps", appNames,
	)
	if err := reconciler.Reconcile(ctx); err != nil {
		p.log.ErrorContext(ctx, "Failed to reconcile.", "error", err)
	}
	go func() {
		for {
			select {
			case newResources = <-watcher.ResourcesC:
				appNames := make([]string, 0, len(newResources))
				for _, r := range newResources {
					appNames = append(appNames, r.GetName())
				}
				p.log.InfoContext(ctx, "Received updated list of app servers from watcher",
					"apps", appNames,
				)
				if err := reconciler.Reconcile(ctx); err != nil {
					p.log.ErrorContext(ctx, "Failed to reconcile.", "error", err)
				}
			case <-ctx.Done():
				p.log.DebugContext(ctx, "Reconciler done.")
				return
			}
		}
	}()
	return nil
}

func (p *proxy) getCurrentResources() map[string]types.AppServer {
	return p.getAppServers()
}

func (p *proxy) getAppServers() map[string]types.AppServer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := map[string]types.AppServer{}
	maps.Copy(out, p.appServers)
	return out
}

func (p *proxy) registerAppServer(ctx context.Context, app types.AppServer) error {
	name := app.GetApp().GetName()
	upstream, err := p.newUpstreamClient(ctx, app)
	if err != nil {
		return trace.Wrap(err, "failed to create upstream MCP client")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.clients[name]; ok {
		p.log.DebugContext(ctx, "upstream MCP client already exists",
			"name", name,
		)
		return nil
	}
	p.clients[name] = upstream
	p.appServers[name] = app
	return nil
}

func (p *proxy) removeAppServer(ctx context.Context, app types.AppServer) error {
	name := app.GetApp().GetName()
	p.mu.Lock()
	defer p.mu.Unlock()
	if upstream, ok := p.clients[name]; ok {
		if err := upstream.Close(); err != nil {
			p.log.DebugContext(ctx, "failed to close upstream MCP client",
				"error", err,
			)
		}
		delete(p.clients, name)
	}
	if _, ok := p.appServers[name]; ok {
		delete(p.appServers, name)
	}
	return nil
}

func (p *proxy) updateAppServer(ctx context.Context, new, old types.AppServer) error {
	if err := p.removeAppServer(ctx, old); err != nil {
		return trace.Wrap(err, "failed to remove old tool for app %s", old.GetName())
	}
	if err := p.registerAppServer(ctx, new); err != nil {
		return trace.Wrap(err, "failed to add new tool for app %s", new.GetName())
	}
	return nil
}

func (p *proxy) newUpstreamClient(ctx context.Context, app types.AppServer) (*Upstream, error) {
	// TODO(gavin): async connect to upstream
	reader, writer, err := p.dialAppServer(ctx, app)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	upstream, err := newUpstream(reader, writer)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = "2024-11-05"
	initReq.Params.ClientInfo.Name = "tsh"
	initReq.Params.ClientInfo.Version = teleport.Version
	_, err = upstream.Initialize(ctx, initReq)
	if err != nil {
		return nil, trace.Wrap(err,
			"failed to initialize upstream %v", app.GetApp().GetName(),
		)
	}
	upstream.OnNotification(func(n mcp.JSONRPCNotification) {
		switch n.Method {
		case NotificationMethodToolsListChanged:
			p.mu.Lock()
			defer p.mu.Unlock()
			tools := p.fetchTools(ctx, upstream)
			p.log.DebugContext(ctx, "Fetching updated tools from upstream",
				"upstream", app.GetApp().GetName(),
			)
			if err := p.addTools(app.GetApp().GetName(), tools); err != nil {
				p.log.DebugContext(ctx,
					"Failed to update MCP server tools list.",
					"upstream", app.GetApp().GetName(),
					"error", err,
				)
				return
			}
		}
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log.DebugContext(ctx, "Fetching initial tools from upstream",
		"upstream", app.GetApp().GetName(),
	)
	tools := p.fetchTools(ctx, upstream)
	if err := p.addTools(app.GetApp().GetName(), tools); err != nil {
		p.log.DebugContext(ctx,
			"Failed to initialize MCP server tools list.",
			"upstream", app.GetApp().GetName(),
			"error", err,
		)
	}
	return upstream, nil
}

func (p *proxy) dialAppServer(ctx context.Context, appServer types.AppServer) (io.ReadCloser, io.WriteCloser, error) {
	return p.cfg.AppDialerFn(ctx, appServer)
}

func (p *proxy) fetchTools(ctx context.Context, upstream *Upstream) []mcp.Tool {
	var cursor mcp.Cursor
	var tools []mcp.Tool
	for {
		req := mcp.ListToolsRequest{}
		req.Params.Cursor = cursor
		res, err := upstream.ListTools(ctx, req)
		if err != nil {
			p.log.DebugContext(ctx,
				"Failed to fetch tools for upstream MCP server",
				"error", err,
			)
			return nil
		}
		tools = append(tools, res.Tools...)
		cursor = res.NextCursor
		if cursor == "" {
			break
		}
	}
	return tools
}

func (p *proxy) addTools(upstreamName string, upstreamTools []mcp.Tool) error {
	p.log.Info("Adding tools from upstream",
		"upstream", upstreamName,
		"tool_count", len(upstreamTools),
	)
	defer p.log.Info("Tools added from upstream",
		"upstream", upstreamName,
		"tool_count", len(upstreamTools),
	)
	serverTools := make([]server.ServerTool, 0, len(upstreamTools))
	for _, upTool := range upstreamTools {
		downTool := upTool
		downTool.Name = p.translateUpstreamTool(upstreamName, upTool.Name)
		serverTools = append(serverTools, server.ServerTool{
			Tool:    downTool,
			Handler: p.callUpstreamTool,
		})
	}
	if len(serverTools) == 0 {
		return nil
	}
	p.server.AddTools(serverTools...)
	return nil
}

func (p *proxy) callUpstreamTool(ctx context.Context, downReq mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	upstreamName, upToolName := p.translateDownstreamTool(downReq.Params.Name)
	var upReq mcp.CallToolRequest
	upReq.Params = downReq.Params
	upReq.Params.Name = upToolName
	p.mu.Lock()
	defer p.mu.Unlock()
	upstream, ok := p.clients[upstreamName]
	if !ok {
		return nil, trace.NotFound("upstream MCP server %v not found", upstreamName)
	}
	return upstream.CallTool(ctx, upReq)
}

// translateUpstreamTool renames an upstream tool in the format "teleport/$app/$name"
func (p *proxy) translateUpstreamTool(upstreamName, toolName string) string {
	return fmt.Sprintf("teleport/%s/%s", upstreamName, toolName)
}

func (p *proxy) translateDownstreamTool(toolName string) (string, string) {
	parts := strings.SplitN(toolName, "/", 3)
	if len(parts) != 3 {
		// should never happen
		p.log.Error("unexpected tool call from downstream client",
			"tool", toolName,
			"parts", parts,
		)
		msg := fmt.Sprintf("the server only advertises namespaced tools, but accepted a tool call (name: %v, parts: %v) that is not namespaced",
			toolName, parts,
		)
		panic(msg)
	}
	upstreamName := parts[1]
	upstreamToolName := parts[2]
	return upstreamName, upstreamToolName
}
