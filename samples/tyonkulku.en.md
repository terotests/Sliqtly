---
title: Workflows as state machines
transition: fade
---

# Workflows as state machines

A ```xstate block draws an XState machine config as a statechart: states, nested states, start dots, events, guards and actions.
{.lead}

## Document review

```xstate
{
  "id": "document-review",
  "initial": "draft",
  "states": {
    "draft": {
      "on": { "SUBMIT": "testing" }
    },
    "testing": {
      "entry": "notifyTesters",
      "on": {
        "ACCEPT": { "target": "accepted", "guard": "role:tester" },
        "REJECT": { "target": "draft", "actions": ["document:addComment"] }
      }
    },
    "accepted": { "type": "final" }
  }
}
```

## Guards are tried in order

```xstate
createMachine({
  id: "Untitled",
  initial: "Initial state",
  states: {
    "Initial state": {
      on: { next: { target: "Another state" } },
    },
    "Another state": {
      on: {
        next: [
          { target: "Parent state", guard: { type: "some condition" } },
          { target: "Initial state" },
        ],
      },
    },
    "Parent state": {
      initial: "Child state",
      on: { back: { target: "Initial state", actions: { type: "reset" } } },
      states: {
        "Child state": { on: { next: { target: "Another child state" } } },
        "Another child state": {},
      },
    },
  },
})
```

## A release, in parallel

```xstate
{
  "id": "release",
  "initial": "planning",
  "on": { "CANCEL": ".cancelled" },
  "states": {
    "planning": { "on": { "START": "development" } },
    "development": {
      "type": "parallel",
      "states": {
        "code": {
          "initial": "writing",
          "states": {
            "writing": { "on": { "PUSH": "review" } },
            "review": { "on": { "APPROVE": "merged", "CHANGES": "writing" } },
            "merged": { "type": "final" }
          }
        },
        "docs": {
          "initial": "draft",
          "states": {
            "draft": { "on": { "PUBLISH": "published" } },
            "published": { "type": "final" }
          }
        }
      },
      "onDone": "testing"
    },
    "testing": {
      "invoke": { "src": "runSuite", "onDone": "released", "onError": "development" },
      "after": { "86400000": { "target": "development", "actions": "escalate" } }
    },
    "released": { "type": "final" },
    "cancelled": { "type": "final" }
  }
}
```
