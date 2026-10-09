---
title: Pelit ja ohjelmat kalvoilla
transition: fade
lang: fi
score: 0
hits-label: Osumat
footer-left: "Pisteet: {score}"
footer-right: "{page} / {pages}"
footer-skip: first
---

# Pelit ja ohjelmat kalvoilla

TypeScript + JSX -ohjelma `app`-lohkossa pyörii omalla kalvollaan
{.lead}

## Scaffold Scramble

```app
src: apps/scaffold.tsx
size: 640x480
```

Klikkaa peliä pelataksesi (← → ↑ ↓, välilyönti). Esc antaa näppäimet takaisin kalvoille, ja kaksoisklikkaus muokkaa sen jälkeen lohkoa.
{.kicker}

## Ohjelma, joka puhuu pakalleen

```app
src: apps/target.tsx
size: 480x270
allow: deck.data, slide.style, slide.nav
```

Osu maaliin viisi kertaa
{#result}

## Näin se kirjoitetaan

```markdown
~~~app
src: apps/scaffold.tsx      # tyylit: apps/scaffold.tsx.css
size: 640x480               # ohjelman omat yksiköt
allow: deck.data, slide.nav # mitä se saa pyytää pakalta
~~~
```

- Ohjelma ajetaan omassa hiekkalaatikossaan: ei sivua, verkkoa eikä tallennusta
- `deck.set("score", 3)` täyttää `{score}`-kohdan ylä- ja alatunnisteessa
- `slide.next()`, `slide.go(n)`: siirtyy kalvolta toiselle
- `el("#result").style({ color: "green" })`: kalvon lohkon ulkoasu
- PDF, PPTX ja pikkukuvat näyttävät ohjelman viimeisen kuvan
