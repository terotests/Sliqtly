---
title: Uutta Sliqtlyssä
transition: fade
footer-left: sliqtly.com
footer-right: "{page} / {pages}"
footer-skip: first last
---

# Uutta Sliqtlyssä {art=waves}

Teemat, taustaefektit ja tavallisista listoista piirretyt asettelut
{.lead}

::: notes
Tämä dekki aukeaa Nebula-teemalla. Vaihda teemaa kohdasta Dia → Teema, niin kaikki diat seuraavat.
:::

## Teeman efekti on CSS:ää

```css
deck {
  fx: starfield;
  fx-hue: 280;
}
figure { colors: #fbbf24 #c084fc #22d3ee #f472b6; }
```

Muokkaa niitä **Teema (CSS)** -välilehdellä; dian oma `{fx=…}` voittaa.
{.kicker}

## Oma efekti yhdelle dialle {fx=ambient-light art=waves}

- `{fx=ambient-light}` otsikossa vaihtaa efektin yhdelle dialle
- `{art=waves}` piirtää viivakuvion sen päälle, tekstin alle
- Viennit saavat molemmat: efektin kuvana, kuvion vektoriviivoina
{.build anim=rise}

## Projekti neljässä vaiheessa

- Suunnittelu: tavoitteet, budjetti ja aikataulu
- Toteutus: koodi, sisältö ja testit
- Pilotti: kymmenen asiakasta kokeilee
- Julkaisu: kaikkien käyttöön
{list-style=process}

## SWOT

- Vahvuudet: vahva brändi
  - kokenut tiimi
- Heikkoudet: pieni myyntitiimi
- Mahdollisuudet: uudet markkinat Pohjoismaissa
- Uhat: isommat kilpailijat
{list-style=swot}

## Matkamme tähän asti

- 2023: Perustettiin Helsingissä
- 2024: Ensimmäinen tuote
- 2025: 10 000 käyttäjää
- 2026: Ulkomaille
{list-style=timeline}

## Neon-kaaviot

```vega-lite
{
  "data": {"values": [
    {"neljännes": "Q1", "käyttäjät": 2.1},
    {"neljännes": "Q2", "käyttäjät": 3.4},
    {"neljännes": "Q3", "käyttäjät": 5.2},
    {"neljännes": "Q4", "käyttäjät": 8.9}
  ]},
  "width": 600,
  "height": 220,
  "mark": "bar",
  "encoding": {
    "x": {"field": "neljännes", "type": "nominal", "title": null, "axis": {"labelAngle": 0}},
    "y": {"field": "käyttäjät", "type": "quantitative", "title": "Käyttäjiä, tuhansia"}
  }
}
```

`chart { chart-style: neon }` teemassa.
{.kicker}

## Kaavat ja emojit 🚀

$$
\text{kasvu} = \frac{8{,}9 - 2{,}1}{2{,}1} \approx 324\,\%
$$

PowerPoint saa kaavan muokattavana yhtälönä.
{.kicker}

## Kiitos {art=waves art-seed=7}
