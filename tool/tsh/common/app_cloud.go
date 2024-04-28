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

package common

import (
	"strings"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/teleport/lib/tlsca"
)

func pickCloudApp(tc *client.TeleportClient, profile *client.ProfileStatus, cf *CLIConf, cloudFriendlyName string, matchRouteToApp func(tlsca.RouteToApp) bool) (proto.RouteToApp, error) {
	if cf.AppName != "" {
		if app, err := findApp(profile.Apps, cf.AppName); err == nil {
			if !matchRouteToApp(*app) {
				return proto.RouteToApp{}, trace.BadParameter(
					"selected app %q is not an %v application", cf.AppName, cloudFriendlyName,
				)
			}
			return tlscaRoutToAppToProto(*app), nil
		} else if !trace.IsNotFound(err) {
			return proto.RouteToApp{}, trace.Wrap(err)
		}

		// If we don't find an active profile for the app, get details from the server.
		app, err := getRegisteredApp(cf, tc)
		if err != nil {
			return proto.RouteToApp{}, trace.Wrap(err)
		}

		routeToApp, err := getRouteToApp(cf, tc, profile, app)
		if err != nil {
			return proto.RouteToApp{}, trace.Wrap(err)
		}

		return routeToApp, nil
	}

	// If a specific app was not requested, check for an active profile matching the type.
	filteredApps := filterApps(matchRouteToApp, profile.Apps)
	switch len(filteredApps) {
	case 1:
		// found 1 match, return it.
		return tlscaRoutToAppToProto(filteredApps[0]), nil
	case 0:
		return proto.RouteToApp{}, trace.BadParameter("please specify an app using --app CLI argument")
	default:
		names := strings.Join(getAppNames(filteredApps), ", ")
		return proto.RouteToApp{}, trace.BadParameter(
			"multiple %v apps are available (%v), please specify one using --app CLI argument", cloudFriendlyName, names,
		)
	}
}

func filterApps(matchRouteToApp func(tlsca.RouteToApp) bool, apps []tlsca.RouteToApp) []tlsca.RouteToApp {
	var out []tlsca.RouteToApp
	for _, app := range apps {
		if matchRouteToApp(app) {
			out = append(out, app)
		}
	}
	return out
}

func getAppNames(apps []tlsca.RouteToApp) []string {
	var out []string
	for _, app := range apps {
		out = append(out, app.Name)
	}
	return out
}

func findApp(apps []tlsca.RouteToApp, name string) (*tlsca.RouteToApp, error) {
	for _, app := range apps {
		if app.Name == name {
			return &app, nil
		}
	}
	return nil, trace.NotFound("failed to find app with %q name", name)
}
