---
title: 3D worlds on slides
transition: fade
matte-label: matte
mirror-label: mirror
lang: en
---

# 3D worlds on slides

Ranger's Three.js port draws a program's `<scene3d>` element, and shiny surfaces mirror the slide's own colours
{.lead}

## The slide in a mirror {art=waves}

```app
src: apps/chrome.tsx
size: 800x450
allow: 3d
```

Click to pause
{.kicker}

## Every shape {art=waves art-seed=7}

```app
src: apps/shapes.tsx
size: 960x300
allow: 3d
```

## How it is written

```markdown
~~~app
src: apps/chrome.tsx
size: 800x450
allow: 3d          # the program may draw a <scene3d> world
~~~
```

- `<mesh shape="knot" metal={0.9} ry={a} />`: box, sphere, torus, knot, cylinder, cone, plane, teapot
- `metal` 0–1: how much the surface mirrors the slide around it
- `<camera z={6} fov={40} />` and `<light kind="sun" x y z />`, otherwise the light comes from the slide's colours
- The background is transparent: the slide shows behind the world
