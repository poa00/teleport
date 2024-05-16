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

import React, { useState, useEffect, useCallback, useRef } from 'react';
import { isBefore, formatDistanceToNowStrict } from 'date-fns';
import styled from 'styled-components';
import { Alert, Box, Flex, Indicator, Text } from 'design';

import { Notification as NotificationIcon, BellRinging } from 'design/Icon';
import Logger from 'shared/libs/logger';
import { useRefClickOutside } from 'shared/hooks/useRefClickOutside';
import { HoverTooltip } from 'shared/components/ToolTip';

import { useInfiniteScroll } from 'shared/hooks';

import { useKeyBasedPagination } from 'shared/hooks/useInfiniteScroll';
import { IGNORE_CLICK_CLASSNAME } from 'shared/hooks/useRefClickOutside/useRefClickOutside';

import { useStore } from 'shared/libs/stores';

import { useTeleport } from 'teleport';
import useStickyClusterId from 'teleport/useStickyClusterId';
import { Dropdown } from 'teleport/components/Dropdown';

import { ButtonIconContainer } from 'teleport/TopBar/Shared';

import { Notification as NotificationType } from 'teleport/services/notifications';

import {
  Notification as AccessListNotification,
  NotificationKind as StoreNotificationKind,
} from 'teleport/stores/storeNotifications';

import { Notification } from './Notification';

const PAGE_SIZE = 15;

const logger = Logger.create('Notifications');

export function Notifications({ iconSize = 24 }: { iconSize?: number }) {
  const ctx = useTeleport();
  const { clusterId } = useStickyClusterId();
  useStore(ctx.storeNotifications);
  // Whether notifications from the local store have been listed already.
  const hasListedLocalNotifications = useRef(false);

  const [userLastSeenNotification, setUserLastSeenNotification] =
    useState<Date>();

  const {
    resources: notifications,
    fetch,
    attempt,
    updateFetchedResources,
  } = useKeyBasedPagination({
    fetchMoreSize: PAGE_SIZE,
    initialFetchSize: PAGE_SIZE,
    fetchFunc: useCallback(
      async paginationParams => {
        const response = await ctx.notificationService.fetchNotifications({
          clusterId,
          startKey: paginationParams.startKey,
          limit: paginationParams.limit,
        });

        setUserLastSeenNotification(response.userLastSeenNotification);

        if (!hasListedLocalNotifications.current) {
          // Access list review reminder notifications aren't currently supported by the native notifications
          // system, and so they won't be returned by the prior request. They are instead generated by the frontend
          // and stored in a store. We therefore need to get those afterwards and add them to the final list
          // here. This only needs to be done on the first notifications fetch.
          const localNotifications = ctx.storeNotifications.getNotifications();
          const processed = localNotifications.map(notif => {
            if (notif.item.kind == StoreNotificationKind.AccessList) {
              return accessListNotifToNotification(notif);
            }
            return null;
          });

          // Set this to true so that we don't do this again on subsequent fetches.
          hasListedLocalNotifications.current = true;

          return {
            agents: [...processed, ...response.notifications],
            startKey: response.nextKey,
          };
        }

        return {
          agents: response.notifications,
          startKey: response.nextKey,
        };
      },
      [clusterId, ctx.notificationService]
    ),
  });

  // Fetch first page on first render.
  useEffect(() => {
    fetch();
  }, []);

  const { setTrigger } = useInfiniteScroll({
    fetch,
  });

  const [view, setView] = useState<View>('All');
  const [open, setOpen] = useState(false);

  const ref = useRefClickOutside<HTMLDivElement>({ open, setOpen });

  function onIconClick() {
    if (!open) {
      setOpen(true);

      if (notifications.length) {
        const latestNotificationTime = notifications[0].createdDate;
        // If the current userLastSeenNotification is already set to the most recent notification's time, don't do anything.
        if (userLastSeenNotification === latestNotificationTime) {
          return;
        }

        const previousLastSeenTime = userLastSeenNotification;

        // Update the visual state right away for a snappier UX.
        setUserLastSeenNotification(latestNotificationTime);

        ctx.notificationService
          .upsertLastSeenNotificationTime(clusterId, {
            time: latestNotificationTime,
          })
          .then(res => setUserLastSeenNotification(res.time))
          .catch(err => {
            setUserLastSeenNotification(previousLastSeenTime);
            logger.error(`Notification last seen time update failed.`, err);
          });
      }
    } else {
      setOpen(false);
    }
  }

  const unseenNotifsCount = notifications.filter(
    notif =>
      isBefore(userLastSeenNotification, notif.createdDate) && !notif.clicked
  ).length;

  function removeNotification(notificationId: string) {
    const notificationsCopy = [...notifications];
    const index = notificationsCopy.findIndex(
      notif => notif.id == notificationId
    );
    notificationsCopy.splice(index, 1);

    updateFetchedResources(notificationsCopy);
  }

  function markNotificationAsClicked(notificationId: string) {
    const newNotifications = notifications.map(notification => {
      return notification.id === notificationId
        ? { ...notification, clicked: true }
        : notification;
    });

    updateFetchedResources(newNotifications);
  }

  return (
    <NotificationButtonContainer
      ref={ref}
      data-testid="tb-notifications"
      className={IGNORE_CLICK_CLASSNAME}
    >
      <HoverTooltip
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
        transformOrigin={{ vertical: 'top', horizontal: 'center' }}
        tipContent="Notifications"
        css={`
          height: 100%;
        `}
      >
        <ButtonIconContainer
          onClick={onIconClick}
          data-testid="tb-notifications-button"
          open={open}
        >
          {unseenNotifsCount > 0 && (
            <UnseenBadge data-testid="tb-notifications-badge">
              {unseenNotifsCount >= 9 ? '9+' : unseenNotifsCount}
            </UnseenBadge>
          )}
          <NotificationIcon
            color={open ? 'text.main' : 'text.muted'}
            size={iconSize}
          />
        </ButtonIconContainer>
      </HoverTooltip>

      <NotificationsDropdown
        open={open}
        data-testid="tb-notifications-dropdown"
      >
        <Header view={view} setView={setView} />
        {attempt.status === 'failed' && (
          <Box px={3}>
            <Alert>Could not load notifications: {attempt.statusText}</Alert>
          </Box>
        )}
        {attempt.status === 'success' && notifications.length === 0 && (
          <EmptyState />
        )}
        <NotificationsList>
          <>
            {!!notifications.length &&
              notifications.map(notif => (
                <Notification
                  notification={notif}
                  key={notif.id}
                  markNotificationAsClicked={markNotificationAsClicked}
                  removeNotification={removeNotification}
                />
              ))}
            {open && <div ref={setTrigger} />}
            {attempt.status === 'processing' && (
              <Flex
                width="100%"
                justifyContent="center"
                alignItems="center"
                mt={2}
              >
                <Indicator />
              </Flex>
            )}
          </>
        </NotificationsList>
      </NotificationsDropdown>
    </NotificationButtonContainer>
  );
}

function Header({
  view,
  setView,
}: {
  view: View;
  setView: (view: View) => void;
}) {
  return (
    <Box
      css={`
        padding: 0px ${p => p.theme.space[3]}px;
        width: 100%;
      `}
    >
      <Flex
        css={`
          flex-direction: column;
          box-sizing: border-box;
          gap: 12px;
          border-bottom: 1px solid
            ${p => p.theme.colors.interactive.tonal.neutral[2]};
          padding-bottom: ${p => p.theme.space[3]}px;
          margin-bottom: ${p => p.theme.space[3]}px;
        `}
      >
        <Text typography="dropdownTitle">Notifications</Text>
        <Flex gap={2}>
          <ViewButton selected={view === 'All'} onClick={() => setView('All')}>
            All
          </ViewButton>
          <ViewButton
            selected={view === 'Unread'}
            onClick={() => setView('Unread')}
          >
            Unread
          </ViewButton>
        </Flex>
      </Flex>
    </Box>
  );
}

function EmptyState() {
  return (
    <Flex
      flexDirection="column"
      alignItems="center"
      justifyContent="center"
      width="100%"
      height="100%"
      mt={4}
      mb={4}
    >
      <Flex
        css={`
          align-items: center;
          justify-content: center;
          height: 88px;
          width: 88px;
          background-color: ${p => p.theme.colors.interactive.tonal.neutral[0]};
          border-radius: ${p => p.theme.radii[7]}px;
          border: 1px solid ${p => p.theme.colors.interactive.tonal.neutral[1]};
        `}
      >
        <BellRinging size={40} />
      </Flex>
      <Text
        mt={4}
        css={`
          font-weight: 500;
          font-size: 18px;
          line-height: 24px;
          text-align: center;
        `}
      >
        You currently have no notifications.
      </Text>
    </Flex>
  );
}

/** accessListNotifToNotification converts an access list notification from the notifications store into the primary
 * Notification type used by the notifications list.
 */
function accessListNotifToNotification(
  accessListNotif: AccessListNotification
): NotificationType {
  const today = new Date();
  const numDays = formatDistanceToNowStrict(accessListNotif.date);

  let titleText;
  if (accessListNotif.date <= today) {
    titleText = `Access list '${accessListNotif.item.resourceName}' was overdue for a review ${numDays} ago.`;
  } else {
    titleText = `Access list '${accessListNotif.item.resourceName}' needs your review within ${numDays}.`;
  }

  return {
    localNotification: true,
    title: titleText,
    id: accessListNotif.id,
    subKind: StoreNotificationKind.AccessList,
    clicked: !!accessListNotif.clicked,
    createdDate: today,
    labels: [{ name: 'redirect-route', value: accessListNotif.item.route }],
  };
}

const NotificationsDropdown = styled(Dropdown)`
  width: 450px;
  padding: 0px;
  padding-top: ${p => p.theme.space[3]}px;
  align-items: center;
  height: 80vh;

  right: -40px;
  @media screen and (min-width: ${p => p.theme.breakpoints.small}px) {
    right: -52px;
  }
  @media screen and (min-width: ${p => p.theme.breakpoints.large}px) {
    right: -140px;
  }
`;

const ViewButton = styled.div<{ selected: boolean }>`
  cursor: pointer;
  align-items: center;
  // TODO(rudream): Clean up radii order in sharedStyles.
  border-radius: 36px;
  display: flex;
  width: fit-content;
  padding: ${p => p.theme.space[2]}px ${p => p.theme.space[3]}px;
  justify-content: space-around;
  font-size: 16px;
  font-weight: 300;
  color: ${props =>
    props.selected
      ? props.theme.colors.text.primaryInverse
      : props.theme.colors.text.muted};
  background-color: ${props =>
    props.selected ? props.theme.colors.brand : 'transparent'};

  .selected {
    color: ${props => props.theme.colors.text.primaryInverse};
    background-color: ${props => props.theme.colors.brand};
    transition: color 0.2s ease-in 0s;
  }
`;

export type View = 'All' | 'Unread';

const NotificationsList = styled.div<{ isScrollbarVisible: boolean }>`
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: ${p => p.theme.space[2]}px;
  width: 100%;
  max-height: 100%;
  overflow-y: auto;
  padding: ${p => p.theme.space[3]}px;
  padding-top: 0px;
  // Subtract the width of the scrollbar from the right padding.
  padding-right: ${p => `${p.theme.space[3] - 8}px`};

  ::-webkit-scrollbar-thumb {
    background-color: ${p => p.theme.colors.interactive.tonal.neutral[2]};
    border-radius: ${p => p.theme.radii[2]}px;
    // Trick to make the scrollbar thumb 2px narrower than the track.
    border: 2px solid transparent;
    background-clip: padding-box;
  }

  ::-webkit-scrollbar {
    width: 8px;
    border-radius: ${p => p.theme.radii[2]}px;
    border-radius: ${p => p.theme.radii[2]}px;
    background-color: ${p => p.theme.colors.interactive.tonal.neutral[0]};
  }

  .notification {
    width: ${p => `${450 - p.theme.space[3] * 2}px`};
  }
`;

const NotificationButtonContainer = styled.div`
  position: relative;
  height: 100%;
`;

const UnseenBadge = styled.div`
  position: absolute;
  width: 16px;
  height: 16px;
  font-size: 10px;
  border-radius: 100%;
  color: ${p => p.theme.colors.text.primaryInverse};
  background-color: ${p => p.theme.colors.buttons.warning.default};
  margin-top: -21px;
  margin-right: -13px;
  display: flex;
  align-items: center;
  justify-content: center;
`;
