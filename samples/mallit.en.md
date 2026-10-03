---
title: Layouts and line art
transition: fade
footer-right: "{page} / {pages}"
footer-skip: first
---

# Layouts and line art {art=waves}

Steps, SWOT and timelines written as lists, drawn in the theme's colours
{.lead}

::: notes
A PRO feature. Every layout below is computed from the text and the room on the slide; change the theme and they change colour with it.
:::

## Project in four steps

```process
- Plan: goals, budget and schedule
- Build: code, content and tests
- Pilot: ten customers try it
- Launch: open to everyone
```

## SWOT

```swot
Strengths: strong brand
  - experienced team
Weaknesses: small sales team
Opportunities: new markets in the Nordics
Threats: larger competitors
```

## Our road so far

```timeline
2023: Founded in Helsinki
2024: First product
2025: 10 000 users
2026: Abroad
```

## How they are written

- A code fence names the layout: `process`, `swot` or `timeline`
- One item per line: `Title: description`; an indented `- point` belongs to the item above
- `{width=60%}` under the fence makes it narrower
- `{art=waves}` on a heading draws line art behind the slide, `art: waves` in the front matter behind every slide, `{art-seed=3}` gives another picture
- Colours from the theme: `figure { colors: #1f6feb #0f9d8a #7c4dff #f08c00 }`
- PDF and PowerPoint get them as vector shapes you can still edit

## Thank you {art=waves art-seed=7}
