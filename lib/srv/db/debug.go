/*
 * Teleport
 * Copyright (C) 2024  Gravitational, Inc.
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

package db

import (
	"context"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/types"
)

type debugServicePlugin struct {
	server *Server
}

func (p *debugServicePlugin) GetProxiedDatabase(ctx context.Context, name string) (types.Database, error) {
	return p.server.getProxiedDatabase(name)
}

func (p *debugServicePlugin) RunHealthCheck(ctx context.Context, name string) (types.Database, error) {
	err := p.server.cfg.DatabaseHealth.RunHealthCheck(ctx, name)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return p.server.getProxiedDatabase(name)
}
