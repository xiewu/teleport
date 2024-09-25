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

package common

import (
	"time"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/utils"
)

func NewConnectivityHealthcheck(connErr error) types.DatabaseHealthCheckV1 {
	if connErr != nil && utils.IsOKNetworkError(connErr) {
		// normal network teardown errors should not indicate unhealthy db
		// connectivity.
		connErr = nil
	}

	hc := types.DatabaseHealthCheckV1{
		Time:    time.Now(),
		Status:  types.DatabaseServerStatus_DATABASE_SERVER_STATUS_HEALTHY,
		Message: "",
	}

	if connErr != nil {
		hc.Status = types.DatabaseServerStatus_DATABASE_SERVER_STATUS_UNHEALTHY
		hc.Message = connErr.Error()
	}

	return hc
}

func PrependHealthCheck(db types.Database, check types.DatabaseHealthCheckV1) {
	health := db.GetStatusHealth()

	checks := append([]*types.DatabaseHealthCheckV1{&check}, health.Checks...)
	if len(checks) > 3 {
		checks = checks[:3]
	}

	health.Checks = checks
	db.SetStatusHealth(health)
}
