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

package debug

import (
	"context"
	"sync"

	"github.com/gravitational/teleport/api/types"
)

type DatabaseServicePlugin interface {
	GetProxiedDatabase(ctx context.Context, name string) (types.Database, error)
	RunHealthCheck(ctx context.Context, name string) (types.Database, error)
}

var dbServicePlugin DatabaseServicePlugin
var pluginMutex = sync.RWMutex{}

// TODO maybe move this to lib/srv/debug/common or lib/srv/debug/plugins
func RegisterDatabaseServicePlugin(p DatabaseServicePlugin) {
	pluginMutex.Lock()
	defer pluginMutex.Unlock()
	dbServicePlugin = p
}

func getDatabaseServicePlugin() DatabaseServicePlugin {
	pluginMutex.RLock()
	defer pluginMutex.RUnlock()
	return dbServicePlugin
}
