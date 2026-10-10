---
title: 3D-maailmat kalvoilla
transition: fade
matte-label: matta
mirror-label: peili
lang: fi
---

# 3D-maailmat kalvoilla

Rangerin Three.js-portti piirtää ohjelman `<scene3d>`-elementin, ja kiiltävät pinnat heijastavat kalvon omia värejä
{.lead}

## Kalvo peilissä {art=waves}

```app
src: apps/chrome.tsx
size: 800x450
allow: 3d
```

Klikkaa pysäyttääksesi
{.kicker}

## Kaikki muodot {art=waves art-seed=7}

```app
src: apps/shapes.tsx
size: 960x300
allow: 3d
```

## Kaivuri {art=waves art-seed=3}

```app
src: apps/excavator.tsx
size: 960x480
allow: 3d
```

Kaivuri on tehty perusmuodoista: `<group>` liikuttaa kaikkea sisällään, joten puomi, varsi ja kauha ovat ryhmiä toistensa sisällä. Klikkaa pysäyttääksesi
{.kicker}

## Näin se kirjoitetaan

```markdown
~~~app
src: apps/chrome.tsx
size: 800x450
allow: 3d          # ohjelma saa piirtää <scene3d>-maailman
~~~
```

- Kirjoitetaan kuten React Three Fiberissä: `<mesh rotation={[0, a, 0]}><torusKnotGeometry args={[1, 0.3]} /><meshStandardMaterial color="#c0c0c0" metalness={0.9} /></mesh>`
- `metalness` 0–1: kuinka paljon pinta peilaa kalvoa ympärillään; kulmat radiaaneina
- `<perspectiveCamera position={[0, 2, 6]} fov={40} />` ja `<directionalLight position={[4, 6, 3]} />`, muuten valot tulevat kalvon väreistä
- Tausta on läpinäkyvä: kalvo näkyy maailman takana
