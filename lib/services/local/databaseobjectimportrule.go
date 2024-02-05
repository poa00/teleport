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

	"github.com/gravitational/trace"

	databaseobjectimportrulev1 "github.com/gravitational/teleport/api/gen/proto/go/teleport/dbobjectimportrule/v1"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/backend"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/teleport/lib/services/local/generic"
)

// databaseObjectImportRuleService manages database object import rules in the backend.
type databaseObjectImportRuleService struct {
	svc generic.ServiceCommon[*databaseobjectimportrulev1.DatabaseObjectImportRule]
}

var _ services.DatabaseObjectImportRule = (*databaseObjectImportRuleService)(nil)

func (s *databaseObjectImportRuleService) UpsertDatabaseObjectImportRule(ctx context.Context, rule *databaseobjectimportrulev1.DatabaseObjectImportRule) error {
	_, err := s.svc.UpsertResource(ctx, rule)
	return trace.Wrap(err)
}

func (s *databaseObjectImportRuleService) UpdateDatabaseObjectImportRule(ctx context.Context, rule *databaseobjectimportrulev1.DatabaseObjectImportRule) (*databaseobjectimportrulev1.DatabaseObjectImportRule, error) {
	out, err := s.svc.UpdateResource(ctx, rule)
	return out, trace.Wrap(err)
}

func (s *databaseObjectImportRuleService) CreateDatabaseObjectImportRule(ctx context.Context, rule *databaseobjectimportrulev1.DatabaseObjectImportRule) (*databaseobjectimportrulev1.DatabaseObjectImportRule, error) {
	out, err := s.svc.CreateResource(ctx, rule)
	return out, trace.Wrap(err)
}

func (s *databaseObjectImportRuleService) GetDatabaseObjectImportRule(ctx context.Context, name string) (*databaseobjectimportrulev1.DatabaseObjectImportRule, error) {
	out, err := s.svc.GetResource(ctx, name)
	return out, trace.Wrap(err)
}

func (s *databaseObjectImportRuleService) DeleteDatabaseObjectImportRule(ctx context.Context, name string) error {
	return trace.Wrap(s.svc.DeleteResource(ctx, name))
}

func (s *databaseObjectImportRuleService) ListDatabaseObjectImportRules(ctx context.Context, size int, token string) ([]*databaseobjectimportrulev1.DatabaseObjectImportRule, string, error) {
	out, next, err := s.svc.ListResources(ctx, size, token)
	return out, next, trace.Wrap(err)
}

const (
	databaseObjectImportRulePrefix = "databaseObjectImportRulePrefix"
)

func NewDatabaseObjectImportRuleService(backend backend.Backend) (services.DatabaseObjectImportRule, error) {
	svc, err := generic.NewService153(backend,
		types.KindDatabaseObjectImportRule,
		databaseObjectImportRulePrefix,
		services.MarshalDatabaseObjectImportRule,
		services.UnmarshalDatabaseObjectImportRule)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &databaseObjectImportRuleService{svc: svc}, nil
}