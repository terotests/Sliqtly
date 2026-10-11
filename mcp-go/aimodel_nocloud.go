//go:build nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import "context"

// a server of one's own built without the cloud has no AI assistant yet
func newAIModel(ctx context.Context) (aiModel, string) { return nil, "" }
