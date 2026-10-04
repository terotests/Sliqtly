//go:build nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

// Built with -tags nocloud: no Firestore, Storage or Firebase Auth, and none
// of Google's client libraries in the binary. The server keeps decks in a
// folder (-data) or nowhere (link mode). packaging/build-deb.sh and the
// Dockerfile's local target build this way.

package main

import (
	"context"
	"errors"
)

func connectFirebase(ctx context.Context, env *Env, projectID, bucket string) error {
	return errors.New("this build has no cloud storage (built with -tags nocloud): run it with -data <folder>")
}
