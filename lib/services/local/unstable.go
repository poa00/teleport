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

package local

import (
	"context"
	"time"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/utils"
)

const assertionTTL = time.Minute * 10

// UnstableService is a catch-all for unstable backend operations related to migrations/compatibility
// that don't fit into, or merit the change of, one of the primary service interfaces.
type UnstableService struct {
	backend.Backend
	*AssertionReplayService
}

// NewUnstableService returns new unstable service instance.
func NewUnstableService(backend backend.Backend, assertion *AssertionReplayService) UnstableService {
	return UnstableService{backend, assertion}
}

func (s UnstableService) AssertSystemRole(ctx context.Context, req proto.UnstableSystemRoleAssertion) error {
	key := systemRoleAssertionsKey(req.ServerID, req.AssertionID)
	item, err := s.Get(ctx, key)
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}

	var set proto.UnstableSystemRoleAssertionSet
	if err == nil {
		if err := utils.FastUnmarshal(item.Value, &set); err != nil {
			return trace.Wrap(err)
		}
	}

	set.ServerID = req.ServerID
	set.AssertionID = req.AssertionID
	set.SystemRoles = append(set.SystemRoles, req.SystemRole)

	newValue, err := utils.FastMarshal(&set)
	if err != nil {
		return trace.Wrap(err)
	}
	newItem := backend.Item{
		Key:     key,
		Value:   newValue,
		Expires: time.Now().Add(assertionTTL).UTC(),
	}
	if item != nil {
		_, err = s.CompareAndSwap(ctx, *item, newItem)
		if trace.IsCompareFailed(err) {
			// nodes are expected to perform assertions sequentially
			return trace.CompareFailed("system role assertion set was concurrently modified (this is bug)")
		}
		return trace.Wrap(err)
	}

	_, err = s.Create(ctx, newItem)
	if trace.IsAlreadyExists(err) {
		// nodes are expected to perform assertions sequentially
		return trace.AlreadyExists("system role assertion set was concurrently created (this is a bug)")
	}
	return trace.Wrap(err)
}

func (s UnstableService) GetSystemRoleAssertions(ctx context.Context, serverID string, assertionID string) (proto.UnstableSystemRoleAssertionSet, error) {
	var set proto.UnstableSystemRoleAssertionSet

	item, err := s.Get(ctx, systemRoleAssertionsKey(serverID, assertionID))
	if err != nil {
		return set, trace.Wrap(err)
	}

	if err := utils.FastUnmarshal(item.Value, &set); err != nil {
		return set, trace.Wrap(err)
	}

	return set, nil
}

func systemRoleAssertionsKey(serverID string, assertionID string) []byte {
	return backend.Key(systemRoleAssertionsPrefix, serverID, assertionID)
}

const (
	systemRoleAssertionsPrefix = "system_role_assertions"
)
