/**
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

import { Text } from 'design';
//import FieldInput from 'shared/components/FieldInput';
import { FieldTextArea } from 'shared/components/FieldTextArea';
import { requiredMatchingRoleNameAndRoleArn } from 'shared/components/Validation/rules';

export function RoleArnInput({
  description,
  roleName,
  roleArn,
  setRoleArn,
  disabled,
}: {
  description?: React.ReactNode;
  roleName: string;
  roleArn: string;
  setRoleArn: (arn: string) => void;
  disabled: boolean;
}) {
  return (
    <>
      {description || (
        <Text>
          Once the script above completes, copy and paste its output down below.
          <Text>
            You can run the script again if you've already closed the window.
          </Text>
        </Text>
      )}
      <FieldTextArea
        mt={3}
        rule={requiredMatchingRoleNameAndRoleArn(roleName)}
        value={roleArn}
        label="Trust Anchor, Profile and Role ARNs"
        placeholder={`arn:aws:rolesanywhere:eu-west-2:123456789012:trust-anchor/00000000-1111-2222-3333-444444444444
arn:aws:rolesanywhere:eu-west-2:123456789012:profile/00000000-1111-2222-3333-555555555555
arn:aws:iam::123456789012:role/MarcoRASyncRole`}
        //width="800px"
        onChange={e => setRoleArn(e.target.value)}
        disabled={disabled}
      />
    </>
  );
}
