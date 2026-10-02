---
title: Neljännesvuosiraportti Q4
transition: fade
header-left: "{title}"
header-right: Esimerkki Oy
footer-left: Luottamuksellinen
footer-right: "{page} / {pages}"
header-skip: first last
footer-skip: first last
---

# Neljännesvuosiraportti Q4

Ylä- ja alaosa, sivunumerot ja niiden tyyli
{.lead}

::: notes
Kansi ja viimeinen dia ovat ilman ylä- ja alaosaa (header-skip, footer-skip).
:::

## Liikevaihto

Liikevaihto kasvoi **7 %** ja oli 18,7 M€.

1. Konsultointi +7 %
2. Palvelut +7 %
3. Tuotteet +4 %
{.build}

## Näin ylä- ja alaosa kirjoitetaan

Front matterissa jokaisella reunalla on kolme paikkaa:
`header-left`, `header-center`, `header-right` ja samat `footer-`-alkuisina.

- `{page}` ja `{pages}` ovat dian numero ja diojen määrä, `{title}` esityksen nimi
- Logo: `header-right: ![](media/logo.png)`
- `footer-skip: first last` jättää kannen ja lopun ilman alaosaa
- Teemassa: `footer { background-color: #1e1b4b; color: #fff; font-weight: bold }`

## Väliotsikko ilman ylä- ja alaosaa {furniture=off}

`{furniture=off}` otsikossa, tai vain `{header=off}` / `{footer=off}`.

## Kiitos

Kysymyksiä?
