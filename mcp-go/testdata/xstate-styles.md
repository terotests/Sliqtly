---
title: Sama idea, monta tyyliä
page: 210x297mm
margin: 20mm
footer-right: "{page} / {pages}"
orientation: landscape
---

# Sama idea, monta tyyliä

Yksi kahvilakäynti.
{.lead}

## XState: oletus

```xstate
{
  "id": "kahvila",
  "initial": "jonossa",
  "states": {
    "jonossa": {
      "after": { "300000": "lahtee" },
      "on": { "VUORO": "tilaa" }
    },
    "tilaa": {
      "on": {
        "TILAA": [
          { "target": "odottaa", "guard": "onRahaa", "actions": ["maksa"] },
          { "target": "lahtee" }
        ]
      }
    },
    "odottaa": {
      "entry": ["aloitaKeitto"],
      "on": { "VALMIS": "nauttii" }
    },
    "nauttii": {
      "on": {
        "KUPPI_TYHJA": [
          { "target": "tilaa", "guard": "haluaaLisaa" },
          { "target": "lahtee" }
        ]
      }
    },
    "lahtee": { "type": "final" }
  }
}
```

## XState: cartoon

```xstate
{
  "id": "kahvila",
  "initial": "jonossa",
  "states": {
    "jonossa": {
      "after": { "300000": "lahtee" },
      "on": { "VUORO": "tilaa" }
    },
    "tilaa": {
      "on": {
        "TILAA": [
          { "target": "odottaa", "guard": "onRahaa", "actions": ["maksa"] },
          { "target": "lahtee" }
        ]
      }
    },
    "odottaa": {
      "entry": ["aloitaKeitto"],
      "on": { "VALMIS": "nauttii" }
    },
    "nauttii": {
      "on": {
        "KUPPI_TYHJA": [
          { "target": "tilaa", "guard": "haluaaLisaa" },
          { "target": "lahtee" }
        ]
      }
    },
    "lahtee": { "type": "final" }
  }
}
```
{style=cartoon}

## XState: rinnakkaiset tilat

```xstate
import { createMachine } from "xstate";

export const machine = createMachine({
  id: "barista",
  initial: "kiinni",
  on: { SAHKOKATKO: ".kiinni" },
  states: {
    kiinni: { on: { AVAA: "auki" } },
    auki: {
      type: "parallel",
      on: { SULJE: "kiinni" },
      states: {
        kone: {
          initial: "lammittyy",
          states: {
            lammittyy: { after: { 10000: "valmis" } },
            valmis: { on: { KEITA: "keittaa" } },
            keittaa: { invoke: { src: "uutaEspresso", onDone: "valmis" } }
          }
        },
        kassa: {
          initial: "vapaa",
          states: {
            vapaa: { on: { ASIAKAS: "palvelee" } },
            palvelee: { exit: ["kiitos"], on: { MAKSETTU: "vapaa" } }
          }
        }
      }
    }
  }
});
```

## XState: rinnakkaiset tilat, romantic

```xstate
import { createMachine } from "xstate";

export const machine = createMachine({
  id: "barista",
  initial: "kiinni",
  on: { SAHKOKATKO: ".kiinni" },
  states: {
    kiinni: { on: { AVAA: "auki" } },
    auki: {
      type: "parallel",
      on: { SULJE: "kiinni" },
      states: {
        kone: {
          initial: "lammittyy",
          states: {
            lammittyy: { after: { 10000: "valmis" } },
            valmis: { on: { KEITA: "keittaa" } },
            keittaa: { invoke: { src: "uutaEspresso", onDone: "valmis" } }
          }
        },
        kassa: {
          initial: "vapaa",
          states: {
            vapaa: { on: { ASIAKAS: "palvelee" } },
            palvelee: { exit: ["kiitos"], on: { MAKSETTU: "vapaa" } }
          }
        }
      }
    }
  }
});
```
{style=romantic}

## XState: virhetesti

```xstate
{
  "id": "rikki",
  "initial": "a",
  "states": {
    "a": { "on": { "MENE": "b", "HUKU": "eiOle" }, "always": { "target": "b", "guard": "heti" } },
    "b": { "type": "final" }
  }
}
```

## Yhteenveto

- XState-kone piirtyy statechartiksi samoilla tyyleillä
