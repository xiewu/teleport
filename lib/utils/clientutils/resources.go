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
	"iter"

	"github.com/gravitational/teleport/api/defaults"
)

// PaginatedResourceIterator returns an iterator that iterates through each
// resource from all pages.
func PaginatedResourceIterator[T any](
	ctx context.Context,
	listPageFunc func(context.Context, int, string) ([]T, string, error),
) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var pageToken string
		for {
			page, nextToken, err := listPageFunc(ctx, defaults.DefaultChunkSize, pageToken)
			// Iterator through page first in case both page and error are
			// returned.
			for _, resource := range page {
				if !yield(resource, nil) {
					return
				}
			}

			if err != nil {
				// We end here regardless.
				var t T
				yield(t, err)
				return
			}

			if nextToken == "" {
				return
			}
			pageToken = nextToken
		}
	}
}
