---
title: Games and programs on slides
transition: fade
score: 0
hits-label: Hits
footer-left: "Score: {score}"
footer-right: "{page} / {pages}"
footer-skip: first
---

# Games and programs on slides

A TypeScript + JSX program in an `app` block runs on its own slide
{.lead}

## Scaffold Scramble

```app
src: apps/scaffold.tsx
size: 640x480
```

Click the game to play it (← → ↑ ↓, Space). Esc gives the keys back to the slides, and a double click then edits the block.
{.kicker}

## Hull Runner

```app
src: apps/hullrun.tsx
size: 640x480
allow: sound
```

Knock out the carrier's reactor cores, then land on the strip (← → thrust and turn, ↑ ↓, Space fires, M music). The sounds are synthesized on the page: `allow: sound`.
{.kicker}

## A program that talks to its deck

```app
src: apps/target.tsx
size: 480x270
allow: deck.data, slide.style, slide.nav
```

Hit the target five times
{#result}

## How it is written

```markdown
~~~app
src: apps/scaffold.tsx      # its stylesheet: apps/scaffold.tsx.css
size: 640x480               # the program's own units
allow: deck.data, slide.nav # what it may ask of the deck
~~~
```

- The program runs in a sandbox of its own: no page, no network, no storage
- `deck.set("score", 3)` fills `{score}` in the header and footer
- `slide.next()`, `slide.go(n)`: moves between slides
- `el("#result").style({ color: "green" })`: the look of a block of the slide
- PDF, PPTX and thumbnails show the program's last picture
