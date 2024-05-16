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

import React from 'react';
import { subSeconds, subMinutes } from 'date-fns';
import { render, screen, userEvent } from 'design/utils/testing';
import { Router } from 'react-router';
import { createMemoryHistory } from 'history';

import { LayoutContextProvider } from 'teleport/Main/LayoutContext';
import TeleportContextProvider from 'teleport/TeleportContextProvider';
import { FeaturesContextProvider } from 'teleport/FeaturesContext';
import { getOSSFeatures } from 'teleport/features';
import TeleportContext, {
  disabledFeatureFlags,
} from 'teleport/teleportContext';
import { makeUserContext } from 'teleport/services/user';
import { mockUserContextProviderWith } from 'teleport/User/testHelpers/mockUserContextWith';
import { makeTestUserContext } from 'teleport/User/testHelpers/makeTestUserContext';
import { NotificationKind } from 'teleport/stores/storeNotifications';

import { clusters } from 'teleport/Clusters/fixtures';

import { NotificationSubKind } from 'teleport/services/notifications';

import { TopBar } from './TopBar';

let ctx: TeleportContext;

beforeAll(() => jest.clearAllMocks());

function setup(): void {
  ctx = new TeleportContext();
  jest
    .spyOn(ctx, 'getFeatureFlags')
    .mockReturnValue({ ...disabledFeatureFlags, assist: true });
  ctx.clusterService.fetchClusters = () => Promise.resolve(clusters);

  ctx.assistEnabled = true;
  ctx.storeUser.state = makeUserContext({
    userName: 'admin',
    cluster: {
      name: 'test-cluster',
      lastConnected: Date.now(),
    },
  });

  global.IntersectionObserver = jest.fn(callback => {
    callback(
      [
        {
          // This is the property that triggers the fetch. We need it to be true.
          isIntersecting: true,
          intersectionRatio: null,
          boundingClientRect: null,
          intersectionRect: null,
          rootBounds: null,
          target: null,
          time: null,
        },
      ],
      null
    );
    return {
      observe: jest.fn(),
      unobserve: jest.fn(),
      disconnect: jest.fn(),
      takeRecords: jest.fn(),
      root: null,
      rootMargin: null,
      thresholds: null,
    };
  });

  mockUserContextProviderWith(makeTestUserContext());
}

test('notification bell without notification', async () => {
  setup();

  render(getTopBar());
  await screen.findByTestId('tb-notifications');

  expect(screen.getByTestId('tb-notifications')).toBeInTheDocument();
  expect(
    screen.queryByTestId('tb-notifications-badge')
  ).not.toBeInTheDocument();
});

test('notification bell with notification', async () => {
  setup();
  ctx.storeNotifications.state = {
    notifications: [
      {
        item: {
          kind: NotificationKind.AccessList,
          resourceName: 'banana',
          route: '',
        },
        id: 'abc',
        date: new Date(),
      },
    ],
  };

  jest.spyOn(ctx.notificationService, 'fetchNotifications').mockResolvedValue({
    nextKey: '',
    userLastSeenNotification: subMinutes(Date.now(), 12), // 12 minutes ago
    notifications: [
      {
        id: '1',
        title: 'Example notification 1',
        subKind: NotificationSubKind.UserCreatedInformational,
        createdDate: subSeconds(Date.now(), 15), // 15 seconds ago
        clicked: false,
        labels: [
          {
            name: 'text-content',
            value: 'This is the text content of the notification.',
          },
        ],
      },
    ],
  });

  jest
    .spyOn(ctx.notificationService, 'upsertLastSeenNotificationTime')
    .mockResolvedValue({
      time: new Date(),
    });

  render(getTopBar());
  await screen.findByTestId('tb-notifications');

  expect(screen.getByTestId('tb-notifications')).toBeInTheDocument();
  expect(screen.getByTestId('tb-notifications-badge')).toBeInTheDocument();
  expect(screen.getByTestId('tb-notifications-badge')).toHaveTextContent('2');

  // Test clicking and rendering of dropdown.
  expect(screen.getByTestId('tb-notifications-dropdown')).not.toBeVisible();

  await userEvent.click(screen.getByTestId('tb-notifications-button'));
  expect(screen.getByTestId('tb-notifications-dropdown')).toBeVisible();
});

const getTopBar = () => {
  return (
    <Router history={createMemoryHistory()}>
      <LayoutContextProvider>
        <TeleportContextProvider ctx={ctx}>
          <FeaturesContextProvider value={getOSSFeatures()}>
            <TopBar />
          </FeaturesContextProvider>
        </TeleportContextProvider>
      </LayoutContextProvider>
    </Router>
  );
};
