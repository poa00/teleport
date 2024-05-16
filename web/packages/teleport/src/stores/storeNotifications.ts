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

import { Store } from 'shared/libs/stores';
import { assertUnreachable } from 'shared/utils/assertUnreachable';

import { KeysEnum } from 'teleport/services/storageService';

export enum NotificationKind {
  AccessList = 'access-list',
}

type AccessListNotification = {
  kind: NotificationKind.AccessList;
  resourceName: string;
  route: string;
};

export type Notification = {
  item: AccessListNotification;
  id: string;
  date: Date;
  clicked?: boolean;
};

// TODO?: based on a feedback, consider representing
// notifications as a collection of maps indexed by id
// which is then converted to a sorted list as needed
// (may be easier to work with)
export type NotificationStoreState = {
  notifications: Notification[];
};

const defaultNotificationStoreState: NotificationStoreState = {
  notifications: [],
};

type LocalNotificationStates = {
  clicked: string[];
  hidden: string[];
};

const defaultLocalNotificationStates: LocalNotificationStates = {
  clicked: [],
  hidden: [],
};

export class StoreNotifications extends Store<NotificationStoreState> {
  state: NotificationStoreState = defaultNotificationStoreState;

  getNotifications(): Notification[] {
    const allNotifs = this.state.notifications;
    const notifStates = this.getNotificationStates();

    if (allNotifs.length === 0) {
      localStorage.removeItem(KeysEnum.LOCAL_NOTIFICATION_STATES);
      return [];
    }

    // Filter out hidden notifications.
    const filtered = allNotifs.filter(notification => {
      return notifStates.hidden.indexOf(notification.id) === -1;
    });

    return filtered.map(notification => {
      // Mark clicked notifications as clicked.
      if (notifStates.clicked.indexOf(notification.id) !== -1) {
        return {
          ...notification,
          clicked: true,
        };
      }
      return notification;
    });
  }

  setNotifications(notices: Notification[]) {
    // Sort by earliest dates.
    const sortedNotices = notices.sort((a, b) => {
      return a.date.getTime() - b.date.getTime();
    });
    this.setState({ notifications: [...sortedNotices] });
  }

  updateNotificationsByKind(notices: Notification[], kind: NotificationKind) {
    switch (kind) {
      case NotificationKind.AccessList:
        const filtered = this.state.notifications.filter(
          n => n.item.kind !== NotificationKind.AccessList
        );
        this.setNotifications([...filtered, ...notices]);
        return;
      default:
        assertUnreachable(kind);
    }
  }

  hasNotificationsByKind(kind: NotificationKind) {
    switch (kind) {
      case NotificationKind.AccessList:
        return this.getNotifications().some(
          n => n.item.kind === NotificationKind.AccessList
        );
      default:
        assertUnreachable(kind);
    }
  }

  getNotificationStates(): LocalNotificationStates {
    const value = window.localStorage.getItem(
      KeysEnum.LOCAL_NOTIFICATION_STATES
    );

    if (!value) {
      return defaultLocalNotificationStates;
    }

    try {
      return JSON.parse(value) as LocalNotificationStates;
    } catch (err) {
      return defaultLocalNotificationStates;
    }
  }

  markNotificationAsClicked(id: string) {
    const currentStates = this.getNotificationStates();

    const updatedStates: LocalNotificationStates = {
      clicked: [...currentStates.clicked, id],
      hidden: currentStates.hidden,
    };

    localStorage.setItem(
      KeysEnum.LOCAL_NOTIFICATION_STATES,
      JSON.stringify(updatedStates)
    );
  }

  markNotificationAsHidden(id: string) {
    const currentStates = this.getNotificationStates();

    const updatedStates: LocalNotificationStates = {
      clicked: currentStates.clicked,
      hidden: [...currentStates.hidden, id],
    };

    localStorage.setItem(
      KeysEnum.LOCAL_NOTIFICATION_STATES,
      JSON.stringify(updatedStates)
    );
  }
}
