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

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"

	"github.com/google/uuid"
	"github.com/gravitational/trace"
	"github.com/mark3labs/mcp-go/mcp"

	apitypes "github.com/gravitational/teleport/api/types"
	apievents "github.com/gravitational/teleport/api/types/events"
	"github.com/gravitational/teleport/lib/authz"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/tlsca"
	"github.com/gravitational/teleport/lib/utils"
	logutils "github.com/gravitational/teleport/lib/utils/log"
)

type mcpServer struct {
	emitter apievents.Emitter
	hostID  string
	log     *slog.Logger
}

// handleConnection handles connection from an MCP application.
func (s *mcpServer) handleConnection(ctx context.Context, clientConn net.Conn, authCtx *authz.Context, app apitypes.Application) error {
	identity := authCtx.Identity.GetIdentity()
	sessionID := uuid.New().String()

	log := s.log.With("session", sessionID)

	log.DebugContext(ctx, "Running mcp",
		"app", app.GetName(),
		"cmd", app.GetMCPCommand(),
		"args", app.GetMCPArgs(),
	)

	mkWriter := func(handleName string, emitEvents bool) *dumpWriter {
		if emitEvents {
			return newDumpWriter(ctx, handleName, s.emitter, log, &identity, sessionID)
		}
		return newDumpWriter(ctx, handleName, nil, log, &identity, sessionID)
	}

	responseWriter := utils.NewSyncWriter(clientConn)
	cmd := exec.CommandContext(ctx, app.GetMCPCommand(), app.GetMCPArgs()...)
	//cmd.Stdin = io.TeeReader(clientConn, mkWriter("in", true))
	cmd.Stdin = &authorizedReader{
		ctx:            ctx,
		clientConn:     clientConn,
		authCtx:        authCtx,
		app:            app,
		responseWriter: responseWriter,
		log:            s.log,
		dumpWriter:     mkWriter("in", true),
	}
	cmd.Stdout = io.MultiWriter(responseWriter, mkWriter("out", false))
	cmd.Stderr = mkWriter("err", false)
	if err := cmd.Start(); err != nil {
		return trace.Wrap(err)
	}
	return cmd.Wait()
}

type authorizedReader struct {
	ctx            context.Context
	clientConn     io.Reader
	authCtx        *authz.Context
	app            apitypes.Application
	responseWriter io.Writer
	log            *slog.Logger
	dumpWriter     *dumpWriter
}

func (r *authorizedReader) Read(p []byte) (n int, err error) {
	temp := make([]byte, len(p))
	n, err = r.clientConn.Read(temp)
	if err != nil {
		return n, trace.Wrap(err)
	}
	if len(temp) != 0 {
		var baseMessage struct {
			ID     any    `json:"id,omitempty"`
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(temp[:n]), &baseMessage); err == nil {
			if baseMessage.ID != nil && baseMessage.Method == string(mcp.MethodToolsCall) {
				r.log.DebugContext(r.ctx, "Tools call", "msg", baseMessage)
				accessState := services.AccessState{
					MFAVerified:    true,
					DeviceVerified: true,
				}
				toolMatcher := &services.MCPToolsMatcher{
					Name: baseMessage.Params.Name,
				}
				authErr := r.authCtx.Checker.CheckAccess(r.app, accessState, toolMatcher)
				if authErr != nil {
					// Send a response.
					result := mcp.CallToolResult{
						Content: []mcp.Content{mcp.TextContent{
							Type: "text",
							Text: fmt.Sprintf("Access denied to this MCP tool: %v. RBAC is enforced by your Teleport roles.", authErr),
						}},
						IsError: false,
					}
					resp := mcp.JSONRPCResponse{
						JSONRPC: mcp.JSONRPC_VERSION,
						ID:      baseMessage.ID,
						Result:  result,
					}
					if respBytes, err := json.Marshal(resp); err == nil {
						// Write response followed by newline
						if _, err := fmt.Fprintf(r.responseWriter, "%s\n", respBytes); err != nil {
							r.log.ErrorContext(r.ctx, "Failed to send JSON RPC response", "error", err, "resp", resp)
						}
					} else {
						r.log.ErrorContext(r.ctx, "Failed to marshal JSON RPC response", "error", err, "resp", resp)
					}
					r.dumpWriter.emitAuditEvent(string(temp[:n]), authErr)
					// Do NOT fail this otherwise the connection will be killed.
					return n, nil
				}
			}
		}
	}
	copy(p, temp)
	r.dumpWriter.Write(temp[:n])
	return n, err
}

func newDumpWriter(ctx context.Context, handleName string, emitter apievents.Emitter, log *slog.Logger, identity *tlsca.Identity, sessionID string) *dumpWriter {
	return &dumpWriter{
		ctx:       ctx,
		logger:    log.With("stdio", handleName),
		emitter:   emitter,
		identity:  identity,
		sessionID: sessionID,
	}
}

type dumpWriter struct {
	ctx       context.Context
	logger    *slog.Logger
	identity  *tlsca.Identity
	emitter   apievents.Emitter
	sessionID string
}

func (d *dumpWriter) emitAuditEvent(msg string, authError error) {
	if d.emitter == nil {
		return
	}

	userMeta := d.identity.GetUserMetadata()
	sessionMeta := apievents.SessionMetadata{SessionID: d.sessionID}
	event, emit, err := mcpMessageToEvent(msg, userMeta, sessionMeta, authError)
	if err != nil {
		d.logger.WarnContext(d.ctx, "Failed to parse RPC message", "error", err)
		return
	}
	if !emit {
		return
	}
	d.logger.InfoContext(d.ctx, "event", "val", event)

	if err := d.emitter.EmitAuditEvent(d.ctx, event); err != nil {
		d.logger.WarnContext(d.ctx, "Failed to emit MCP call event.", "error", err)
	}
}

func (d *dumpWriter) Write(p []byte) (int, error) {
	d.emitAuditEvent(string(p), nil)
	d.logger.Log(d.ctx, logutils.TraceLevel, "=== dump", "data", string(p))
	return len(p), nil
}
