/**
 * Teleport
 * Copyright (C) 2025 Gravitational, Inc.
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

import { useState } from 'react';

import { DesktopSession } from 'shared/components/DesktopSession';
import { makeSuccessAttempt } from 'shared/hooks/useAsync';
import { TdpClient } from 'shared/libs/tdp';

import { cloneAbortSignal } from 'teleterm/services/tshd';
import { useAppContext } from 'teleterm/ui/appContextProvider';
import Document from 'teleterm/ui/Document';
import * as types from 'teleterm/ui/services/workspacesService';
import { routing } from 'teleterm/ui/uri';

const noOtherSession = () => Promise.resolve(false);
const acl = makeSuccessAttempt({
  clipboardSharingEnabled: true,
  directorySharingEnabled: true,
});

export function DocumentDesktopSession(props: {
  visible: boolean;
  doc: types.DocumentDesktopSession;
}) {
  const { desktopUri, login } = props.doc;
  const appCtx = useAppContext();

  const [client] = useState(
    () =>
      new TdpClient(async abortSingal => {
        const stream = appCtx.tshd.connectDesktop({
          abort: cloneAbortSignal(abortSingal),
        });
        await stream.requests.send({
          init: {
            desktopUri,
            login,
          },
        });

        return {
          onMessage: callback =>
            stream.responses.onMessage(message =>
              callback(
                message.data.buffer.slice(
                  message.data.byteOffset,
                  message.data.byteOffset + message.data.byteLength
                )
              )
            ),
          onError: (...args) => stream.responses.onError(...args),
          onComplete: (...args) => stream.responses.onComplete(...args),
          send: data => {
            // Strings are sent only in the session player.
            if (typeof data === 'string') {
              return;
            }
            return stream.requests.send({
              data: { data: new Uint8Array(data) },
            });
          },
        };
      })
  );

  return (
    <Document visible={props.visible}>
      <DesktopSession
        hasAnotherSession={noOtherSession}
        desktop={
          routing.parseWindowsDesktopUri(desktopUri).params?.windowsDesktopId
        }
        client={client}
        username={login}
        aclAttempt={acl}
      />
    </Document>
  );
}
