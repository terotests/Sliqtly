---
title: Asettelut ja viivakuviot
transition: fade
footer-right: "{page} / {pages}"
footer-skip: first
---

# Asettelut ja viivakuviot {art=waves}

Vaiheet, SWOT ja aikajanat listoina, piirrettynä teeman väreillä
{.lead}

::: notes
PRO-ominaisuus. Jokainen alla oleva asettelu lasketaan tekstistä ja dian tilasta; vaihda teemaa, niin värit vaihtuvat mukana.
:::

## Projekti neljässä vaiheessa

```process
- Suunnittelu: tavoitteet, budjetti ja aikataulu
- Toteutus: koodi, sisältö ja testit
- Pilotti: kymmenen asiakasta kokeilee
- Julkaisu: kaikkien käyttöön
```

## SWOT

```swot
Vahvuudet: vahva brändi
  - kokenut tiimi
Heikkoudet: pieni myyntitiimi
Mahdollisuudet: uudet markkinat Pohjoismaissa
Uhat: isommat kilpailijat
```

## Matkamme tähän asti

```timeline
2023: Perustettiin Helsingissä
2024: Ensimmäinen tuote
2025: 10 000 käyttäjää
2026: Ulkomaille
```

## Näin ne kirjoitetaan

- Koodilohkon kieli nimeää asettelun: `process`, `swot` tai `timeline`
- Yksi kohta riviä kohden: `Otsikko: kuvaus`; sisennetty `- kohta` kuuluu yllä olevaan
- `{width=60%}` fencen alla kaventaa sitä
- `{art=waves}` otsikossa piirtää viivakuvion dian taakse, `art: waves` front matterissa jokaisen dian taakse, `{art-seed=3}` antaa toisen kuvan
- Värit teemasta: `figure { colors: #1f6feb #0f9d8a #7c4dff #f08c00 }`
- PDF ja PowerPoint saavat ne vektorimuotoina, joita voi yhä muokata

## Kiitos {art=waves art-seed=7}
