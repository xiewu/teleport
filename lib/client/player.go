/*
 * Teleport
 * Copyright (C) 2023  Gravitational, Inc.
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

package client

import (
	"context"
	"fmt"
	"os"

	"github.com/gravitational/trace"

	apievents "github.com/gravitational/teleport/api/types/events"
	"github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/session"
)

// playFromFileStreamer implements [player.Streamer] for
// streaming from a local file.
type playFromFileStreamer struct {
	filename string
}

func (p *playFromFileStreamer) StreamSessionEvents(
	ctx context.Context,
	sessionID session.ID,
	startIndex int64,
) (chan apievents.AuditEvent, chan error) {
	evts := make(chan apievents.AuditEvent)
	errs := make(chan error, 1)

	go func() {
		f, err := os.Open(p.filename)
		if err != nil {
			errs <- trace.ConvertSystemError(err)
			return
		}
		defer f.Close()

		pr := events.NewProtoReader(f)
		for i := int64(0); ; i++ {
			evt, err := pr.Read(ctx)
			if err != nil {
				errs <- trace.Wrap(err)
				return
			}

			if printEvt, ok := evt.(*apievents.SessionPrint); ok {
				fmt.Fprintf(
					os.Stderr,
					"i=%d: Decoded print event: ci=%d offset=%d delay=%d data=[%s]\n",
					i, printEvt.ChunkIndex, printEvt.Offset, printEvt.DelayMilliseconds,
					printEvt.Data,
				)
				if b1, b2 := int(printEvt.Bytes), len(printEvt.Data); b1 != b2 {
					fmt.Fprintf(os.Stderr, "BYTES DIFFER! %d vs %d\n", b1, b2)
				}
			} else {
				fmt.Fprintf(
					os.Stderr,
					"i=%d: Decoded event: type=%q, code=%q\n",
					i, evt.GetType(), evt.GetCode(),
				)
			}

			if true {
				continue
			}

			if i >= startIndex {
				select {
				case evts <- evt:
				case <-ctx.Done():
					errs <- trace.Wrap(ctx.Err())
					return
				}
			}
		}
	}()

	return evts, errs
}
