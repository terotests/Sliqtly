//go:build !nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/terotests/sliqtly/mcp-go/store"
	"github.com/terotests/sliqtly/mcp-go/store/storetest"
)

// Firestore's emulator (FIRESTORE_EMULATOR_HOST, as `firebase
// emulators:exec` sets it): the tests are skipped without one
func emulatorClient(t *testing.T) *firestore.Client {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("no Firestore emulator (FIRESTORE_EMULATOR_HOST)")
	}
	c, err := firestore.NewClient(context.Background(), "demo-sliqtly")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

var fsTestRun atomic.Int64

// collections of their own for each test
func fsTestPrefix() string {
	return fmt.Sprintf("t%d_%d_", time.Now().UnixNano(), fsTestRun.Add(1))
}

func TestFSEngine(t *testing.T) {
	c := emulatorClient(t)
	storetest.Run(t, func(t *testing.T) store.Engine { return newFSEngine(c, fsTestPrefix()) })
}

func TestFSChat(t *testing.T) {
	c := emulatorClient(t)
	storetest.RunChat(t, func(t *testing.T) store.ChatLog { return newFSChat(c, fsTestPrefix()) })
}

// the cloud's rooms as sliqtly.com keeps them
func TestCloudRoomsFirestore(t *testing.T) {
	c := emulatorClient(t)
	p := fsTestPrefix()
	cloudRoomsFlow(t, newFSEngine(c, p), newFSChat(c, p))
}
