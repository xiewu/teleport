/**
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

import { useState } from 'react';
import { Link as InternalRouteLink } from 'react-router-dom';
import styled from 'styled-components';

import { Box, ButtonPrimary, ButtonSecondary, Flex, Link, Text } from 'design';
import Dialog, {
  DialogContent,
  DialogFooter,
  DialogHeader,
  //DialogHeader,
  DialogTitle,
} from 'design/Dialog';
import * as Icons from 'design/Icon';
import FieldInput from 'shared/components/FieldInput';
import Validation from 'shared/components/Validation';
import { requiredIamRoleName } from 'shared/components/Validation/rules';

import cfg from 'teleport/config';
import { Header } from 'teleport/Discover/Shared';
import {
  RoleArnInput,
  ShowConfigurationScript,
} from 'teleport/Integrations/shared';
import { AwsOidcPolicyPreset } from 'teleport/services/integrations';

import { SectionBox } from '../../../Roles/RoleEditor/StandardEditor/sections';
import { ConfigureAwsOidcSummary } from './ConfigureAwsOidcSummary';
import { FinishDialog } from './FinishDialog';
import { useAwsOidcIntegration } from './useAwsOidcIntegration';

export function AwsOidc() {
  const {
    integrationConfig,
    setIntegrationConfig,
    scriptUrl,
    setScriptUrl,
    handleOnCreate,
    createdIntegration,
    createIntegrationAttempt,
    generateAwsOidcConfigIdpScript,
  } = useAwsOidcIntegration();

  const [isModalOpen, setIsModalOpen] = useState(false);

  const handleCreateIntegration = validator => {
    handleOnCreate(validator);
    setIsModalOpen(true); // Open the modal after clicking the button
  };

  return (
    <Box pt={3}>
      <Header>AWS CLI/Console Access</Header>

      <Box width="800px" mb={4}>
        Compatible with any CLI and AWS SDK-based tooling (includes Terraform,
        AWS CLI, ) Teleport uses{' '}
        <Text as="span" bold>
          AWS IAM Roles Anywhere
        </Text>{' '}
        to manage access and allows you to configure the right permissions for
        your users.
      </Box>

      <Box width="800px" mb={4}>
        <Link
          target="_blank"
          href={'https://console.aws.amazon.com/rolesanywhere/home/'}
        >
          <Text as="span" bold>
            Create Profiles
          </Text>
        </Link>{' '}
        and assign Roles to them, Teleport will allow you to configure access
        based on these Profiles.
      </Box>

      <Box width="800px" mb={4}>
        Follow the below steps to create a Roles Anywhere Trust Anchor and
        configure the required IAM Roles for synchronizing Profiles as Teleport
        resources.
      </Box>

      <Validation>
        {({ validator }) => (
          <>
            <Container mb={5} width={800}>
              <Text bold>Step 1</Text>
              <Box width="600px">
                <FieldInput
                  autoFocus={true}
                  value={integrationConfig.name}
                  label="Give this AWS integration a name"
                  placeholder="Integration Name"
                  onChange={e =>
                    setIntegrationConfig({
                      ...integrationConfig,
                      name: e.target.value,
                    })
                  }
                  disabled={!!scriptUrl}
                />
                <SectionBox
                  titleSegments={[
                    'Optionally, edit AWS resource names to create',
                  ]}
                  isProcessing={false}
                  tooltip="closed"
                >
                  <FieldInput
                    rule={requiredIamRoleName}
                    value={integrationConfig.trustAnchor}
                    placeholder="Trust Anchor name"
                    label="Give a name for the Roles Anywhere Trust Anchor this integration will create"
                    onChange={e =>
                      setIntegrationConfig({
                        ...integrationConfig,
                        trustAnchor: e.target.value,
                      })
                    }
                    disabled={!!scriptUrl}
                  />
                  <FieldInput
                    rule={requiredIamRoleName}
                    value={integrationConfig.roleName}
                    placeholder="Profile Sync: Role name"
                    label="Give a name for an AWS IAM role this integration will create in order to keep Profiles in sync"
                    onChange={e =>
                      setIntegrationConfig({
                        ...integrationConfig,
                        roleName: e.target.value,
                      })
                    }
                    disabled={!!scriptUrl}
                  />
                  <FieldInput
                    rule={requiredIamRoleName}
                    value={integrationConfig.profileName}
                    placeholder="Profile Sync: Profile name"
                    label="Give a name for an AWS IAM Roles Anywhere Profile this integration will create in order to keep Profiles in sync"
                    onChange={e =>
                      setIntegrationConfig({
                        ...integrationConfig,
                        profileName: e.target.value,
                      })
                    }
                    disabled={!!scriptUrl}
                  />
                </SectionBox>
              </Box>
              {scriptUrl ? (
                <ButtonSecondary
                  mb={3}
                  onClick={() => {
                    setScriptUrl('');
                  }}
                >
                  Edit
                </ButtonSecondary>
              ) : (
                <ButtonSecondary
                  mb={3}
                  onClick={() =>
                    generateAwsOidcConfigIdpScript(
                      validator,
                      AwsOidcPolicyPreset.Unspecified
                    )
                  }
                >
                  Generate Command
                </ButtonSecondary>
              )}
            </Container>
            {scriptUrl && (
              <>
                <Container mb={5} width={800}>
                  <Flex gap={1} alignItems="center">
                    <Text bold>Step 2</Text>
                    <ConfigureAwsOidcSummary
                      roleName={integrationConfig.roleName}
                      integrationName={integrationConfig.name}
                    />
                  </Flex>
                  <ShowConfigurationScript scriptUrl={scriptUrl} />
                </Container>
                <Container mb={5} width={800}>
                  <Text bold>Step 3</Text>
                  <RoleArnInput
                    roleName={integrationConfig.roleName}
                    roleArn={integrationConfig.roleArn}
                    setRoleArn={(v: string) =>
                      setIntegrationConfig({
                        ...integrationConfig,
                        roleArn: v,
                      })
                    }
                    disabled={createIntegrationAttempt.status === 'processing'}
                  />
                </Container>
              </>
            )}
            {createIntegrationAttempt.status === 'error' && (
              <Flex>
                <Icons.Warning mr={2} color="error.main" size="small" />
                <Text color="error.main">
                  Error: {createIntegrationAttempt.statusText}
                </Text>
              </Flex>
            )}
            <Box mt={6}>
              {/* <ButtonPrimary
                onClick={() => handleCreateIntegration(validator)}
                disabled={
                  !scriptUrl ||
                  createIntegrationAttempt.status === 'processing' ||
                  !integrationConfig.roleArn
                }
              >
                Create Integration-old
              </ButtonPrimary> */}
              <ButtonPrimary
                onClick={() => setIsModalOpen(true)}
                disabled={
                  !scriptUrl ||
                  createIntegrationAttempt.status === 'processing' ||
                  !integrationConfig.roleArn
                }
              >
                Configure Access
              </ButtonPrimary>
              <ButtonSecondary
                ml={3}
                as={InternalRouteLink}
                to={cfg.getIntegrationEnrollRoute(null)}
              >
                Back
              </ButtonSecondary>
            </Box>
          </>
        )}
      </Validation>
      {createdIntegration && <FinishDialog integration={createdIntegration} />}

      {/* Modal for Create Integration */}
      {isModalOpen && (
        <Dialog onClose={() => setIsModalOpen(false)} open={isModalOpen}>
          <DialogTitle>AWS IAM Roles Anywhere Profiles</DialogTitle>
          <DialogHeader>
            Teleport periodically checks for new Profiles and will add them to
            your cluster as they are created
          </DialogHeader>
          <DialogContent width={800}>
            <Box>
              {[
                {
                  name: 'MarcoRA-RO-S3',
                  tags: [
                    { key: 'Department', value: 'Engineering' },
                    { key: 'Environment', value: 'Development' },
                  ],
                  roles: [
                    'MarcoRA-RO-S3-Role',
                    'MarcoRA-RW-S3-Role',
                    'MarcoRA-777-S3-Role',
                  ],
                },
                {
                  name: 'MarcoRA-RO-EC2',
                  tags: [
                    { key: 'Department', value: 'Operations' },
                    { key: 'CostCenter', value: 'CC-1234' },
                  ],
                  roles: ['MarcoRA-RO-EC2-Role'],
                },
              ].map((profile, i) => (
                <Box
                  key={i}
                  mb={3}
                  p={3}
                  borderRadius={2}
                  border="2px solid"
                  borderColor="primary.main"
                  bg="spotBackground[1]"
                  style={{ cursor: 'pointer' }}
                >
                  <Text bold mb={2}>
                    {profile.name}
                  </Text>

                  {profile.tags.length > 0 && (
                    <Flex mb={2} flexWrap="wrap" gap={2}>
                      {profile.tags.map((tag, j) => (
                        <Box
                          key={j}
                          px={3}
                          py={1}
                          bg="spotBackground[1]"
                          borderRadius={16}
                          border="1px solid"
                          borderColor="border"
                        >
                          <Flex alignItems="center">
                            <Text
                              as="span"
                              fontSize="text.small"
                              fontWeight="bold"
                              mr={1}
                            >
                              {tag.key}:
                            </Text>
                            <Text as="span" fontSize="text.small">
                              {tag.value}
                            </Text>
                          </Flex>
                        </Box>
                      ))}
                    </Flex>
                  )}

                  <Text fontSize="text.small" mt={2} mb={1}>
                    Roles:
                  </Text>
                  <Flex flexDirection="column">
                    {profile.roles.map((role, k) => (
                      <Link
                        key={k}
                        onClick={() =>
                          alert(`Selected ${role} from ${profile.name}`)
                        }
                        style={{ cursor: 'pointer' }}
                        mb={1}
                      >
                        {role}
                      </Link>
                    ))}
                  </Flex>
                </Box>
              ))}
            </Box>
          </DialogContent>
          <DialogFooter>
            <ButtonSecondary
              onClick={() => setIsModalOpen(false)}
              onAbort={handleCreateIntegration}
            >
              Edit
            </ButtonSecondary>
            <ButtonPrimary onClick={() => setIsModalOpen(false)}>
              Configure Access
            </ButtonPrimary>
          </DialogFooter>
        </Dialog>
      )}
    </Box>
  );
}

const Container = styled(Box)`
  max-width: 1000px;
  background-color: ${p => p.theme.colors.spotBackground[0]};
  border-radius: ${p => `${p.theme.space[2]}px`};
  padding: ${p => p.theme.space[3]}px;
`;
