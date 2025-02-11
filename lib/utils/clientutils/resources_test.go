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

package clientutils

import (
	"context"
	"testing"

	"github.com/gravitational/trace"
	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/defaults"
)

type mockPaginator struct {
	errorOnPage1 bool
	errorOnPage2 bool
}

func markEvenIndexesTrue(s []bool) []bool {
	for i := range s {
		if i%2 == 0 {
			s[i] = true
		}
	}
	return s
}

func (m *mockPaginator) List(_ context.Context, pageSize int, token string) ([]bool, string, error) {
	switch token {
	case "":
		if m.errorOnPage1 {
			return nil, "", trace.NotFound("page1 failed")
		}
		return markEvenIndexesTrue(make([]bool, pageSize)), "page2", nil
	case "page2":
		if m.errorOnPage2 {
			return nil, "", trace.NotFound("page2 failed")
		}
		return markEvenIndexesTrue(make([]bool, pageSize)), "page3", nil
	case "page3":
		return markEvenIndexesTrue(make([]bool, 4)), "", nil
	default:
		return nil, "", trace.BadParameter("invalid token")
	}
}

func TestPaginatedResourceIterator(t *testing.T) {
	paginatorSuccess := &mockPaginator{}
	paginatorPage1Error := &mockPaginator{errorOnPage1: true}
	paginatorPage2Error := &mockPaginator{errorOnPage2: true}

	tests := []struct {
		name            string
		paginator       func(context.Context, int, string) ([]bool, string, error)
		expectTrueCount int
		checkError      require.ErrorAssertionFunc
	}{
		{
			name:      "pagination success",
			paginator: paginatorSuccess.List,
			// Half of two pages plus four items are true.
			expectTrueCount: (defaults.DefaultChunkSize*2 + 4) / 2,
			checkError:      require.NoError,
		},
		{
			name:            "page1 failure",
			paginator:       paginatorPage1Error.List,
			expectTrueCount: 0,
			checkError:      require.Error,
		},
		{
			name:      "page2 failure",
			paginator: paginatorPage2Error.List,
			// Half of one page are true.
			expectTrueCount: defaults.DefaultChunkSize / 2,
			checkError:      require.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var outputError error
			var trueCount int
			for item, err := range PaginatedResourceIterator(context.Background(), tt.paginator) {
				if err != nil {
					outputError = err
					break
				}
				if item {
					trueCount++
				}
			}
			tt.checkError(t, outputError)
			require.Equal(t, tt.expectTrueCount, trueCount, tt.name)
		})
	}
}
