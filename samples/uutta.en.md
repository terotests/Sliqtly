---
title: What's new in Sliqtly
transition: fade
footer-left: sliqtly.com
footer-right: "{page} / {pages}"
footer-skip: first last
---

# What's new in Sliqtly {art=waves}

Themes, background effects and layouts drawn from plain lists
{.lead}

::: notes
This deck opens on the Nebula theme. Change the theme from Slide → Theme and every slide follows.
:::

## The theme's effect is CSS

```css
deck {
  fx: starfield;
  fx-hue: 280;
}
figure { colors: #fbbf24 #c084fc #22d3ee #f472b6; }
```

Edit them in the **Theme (CSS)** tab; a slide's own `{fx=…}` wins.
{.kicker}

## A slide of its own {fx=ambient-light art=waves}

- `{fx=ambient-light}` on a heading changes the effect for one slide
- `{art=waves}` draws line art over it, under the text
- Exports get both: the effect as a picture, the art as vector lines
{.build anim=rise}

## Rain on the glass {fx=drops}

- `{fx=drops}`: drops land, run together and run down the glass
- `fx-rain=2` for a downpour, `fx-size=1.5` for bigger drops
- Round drops that stay put: `{fx=bubbles}`

## Project in four steps

- Plan: goals, budget and schedule
- Build: code, content and tests
- Pilot: ten customers try it
- Launch: open to everyone
{list-style=process}

## SWOT

- Strengths: strong brand
  - experienced team
- Weaknesses: small sales team
- Opportunities: new markets in the Nordics
- Threats: larger competitors
{list-style=swot}

## Our road so far

- 2023: Founded in Helsinki
- 2024: First product
- 2025: 10 000 users
- 2026: Abroad
{list-style=timeline}

## Neon charts

```vega-lite
{
  "data": {"values": [
    {"quarter": "Q1", "users": 2.1},
    {"quarter": "Q2", "users": 3.4},
    {"quarter": "Q3", "users": 5.2},
    {"quarter": "Q4", "users": 8.9}
  ]},
  "width": 600,
  "height": 220,
  "mark": "bar",
  "encoding": {
    "x": {"field": "quarter", "type": "nominal", "title": null, "axis": {"labelAngle": 0}},
    "y": {"field": "users", "type": "quantitative", "title": "Users, thousands"}
  }
}
```

`chart { chart-style: neon }` in the theme.
{.kicker}

## Formulas and emoji 🚀

$$
\text{growth} = \frac{8.9 - 2.1}{2.1} \approx 324\,\%
$$

PowerPoint gets the formula as an equation you can edit.
{.kicker}

## Thank you {art=waves art-seed=7}
