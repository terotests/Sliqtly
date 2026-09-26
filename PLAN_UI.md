# EVG Presentation: UI-suunnitelma

Markdown → agentti → EVG-slideshow (efektit, puhe) → toisto sivulla → vienti PDF/PPTX.
Vasemmalla Markdown, oikealla esitys, jossa jokaisella dialla on kolme raitaa: **teksti**, **puhe**, **animaatio**.

---

## Sijainti

Sovellus rakennetaan tähän repoon (EVGPresentation). Ranger (`gallery/markdown`, `gallery/pptx`, `lib/evg`) ja EvgHarness otetaan riippuvuuksiksi samalla tavalla kuin EvgHarness tekee (`scripts/setup.mjs` kloonaa `.deps/`-hakemistoon). Rangeriin tehdään vain yleiskäyttöiset muutokset (syntaksi, `MdToPptx`-laajennukset, `EVGTimeline`, PPTX-audio).

---

## 1. Lähtökohta: mitä on jo olemassa

| Osa | Missä | Käyttö tässä |
| --- | --- | --- |
| Markdown-editori (selain, WYSIWYG-esikatselu) | `Ranger/gallery/markdown/web/markdown_web.rgr` (`MarkdownEdit`), `web/standalone/` | Vasen paneeli ja slides-asettelu pohjaksi |
| Slide-asettelu | `MdLayout.layoutSlides`, `{.slide}`, `{.c3}`, front matter (`theme`, `page`) | Dian rajat ja perusasettelu |
| Markdown → EVG | `MdToEvg` → `EVGDisplayList` → `lib/evg/gl/evg-webgl.js` | Dian piirto |
| Klikkaus → lähdekoodi | `MdSrcMap`, `MdLayout.srcAtPoint`, `MdSemanticEdit` | Esityksestä editointi takaisin Markdowniin |
| PDF-vienti | `MarkdownEdit.pdf()`, `EVGPDFRenderer` | Sellaisenaan |
| PPTX-vienti | `MdToPptx.deck`, `md_deck_tool` | Sellaisenaan, laajennetaan |
| PPTX-malli: muistiinpanot, siirtymät, build-animaatiot | `PptxSlide.notes`, `PptxTransition`, `PptxBuildStep` (`gallery/pptx/src/PptxModel.rgr`, `PptxWriter.rgr`) | Tuettu kirjastossa, **MdToPptx ei vielä aseta niitä** |
| Värien/lukujen transitiot | `lib/evg/EVGTransition.rgr`, `EVGEasing.rgr` | Animaatioiden interpolointi |
| Agentti + live-rakennus | `EvgHarness/livebuild` (EVGPatch-opit SSE:llä, Gemini/Claude/Codex) | Agentin runko |

Puuttuu: keyframe/aikajana-malli (vain suunnitelma `gallery/evg_video/PLAN_EVG_VIDEO.md`), toistomoottori, ääni/TTS, audio PPTX:ssä, Markdown-syntaksi muistiinpanoille/siirtymille/build-askelille.

---

## 2. Näkymä

```
┌──────────────────────────────────────────────────────────────────────────────────────┐
│ ◧ deck.md   Teema [corporate ▾]  16:9 ▾   ✦ Generoi  ▶ Esitä   ⤓ Vie [PDF|PPTX|HTML] │
├───────────────────────────────┬──────────────────────────────────────────────────────┤
│ MARKDOWN                      │ ESITYS                                               │
│                               │ ┌──────────────────────────────────────────────────┐ │
│ ## Miten botti puhuu? {#bot}  │ │                                                  │ │
│    {transition=fade}          │ │           (valittu dia, EVG/WebGL)               │ │
│ ▌                             │ │   klikkaus → valitsee elementin + kursori        │ │
│ - Liittyy vieraana {.build}   │ │   vasemmalle vastaavaan kohtaan                  │ │
│ - Kuulee äänen                │ │                                                  │ │
│ - Vastaus mikrofonina         │ └──────────────────────────────────────────────────┘ │
│                               │  ◀ 3 / 12 ▶     ⏮ ▶ ⏭   00:41 / 06:10   🔊 ━━━○──     │
│ ::: notes                     │ ┌──────────────────────────────────────────────────┐ │
│ Botti liittyy kuten kuka      │ │ TEKSTI   ▏Otsikko▏ •1 ▏ •2 ▏ •3 ▏ •4 ▏            │ │
│ tahansa [[1]] ja kuulee...    │ │ PUHE     ▏▁▂▅▇▅▂▁▂▅▇▇▅▂▁▁▂▅▇▅▂▁▏ 🎙 ⤒ ✦TTS        │ │
│ :::                           │ │ ANIM     ▏fade-in▏  ▏build▏ ▏build▏ ▏pulse──────▏  │ │
│                               │ └──────────────────────────────────────────────────┘ │
│                               │ ┌────┐┌────┐┌────┐┌────┐┌────┐                       │
│                               │ │ 1  ││ 2  ││▣3  ││ 4  ││ 5  │ … (filmstrip, drag)   │
│                               │ └────┘└────┘└────┘└────┘└────┘                       │
├───────────────────────────────┴──────────────────────────────────────────────────────┤
│ Agentti: "Dia 3: lisätty aaltoanimaatio, 4 build-askelta, puhe 38 s"   [Hylkää] [OK] │
└──────────────────────────────────────────────────────────────────────────────────────┘
```

### Paneelit

- **Vasen, Markdown.** Nykyinen editori. Kursori seuraa valittua diaa ja päinvastoin (dian vaihto esityksessä vierittää lähteen `MdSrcMap`illa). Gutterissa ikonit: 🔊 = dialla on ääni, ✦ = agentin muokkaama, 🔒 = lukittu (agentti ei koske).
- **Oikea ylä, lava.** Valittu dia oikeassa kuvasuhteessa. Kaksi tilaa: *muokkaa* (WYSIWYG, elementin valinta, raahaus) ja *toista* (aikajana käy).
- **Oikea keski, raidat** (valitun dian aikajana, vaakasuunnassa sekunteja):
  - *Teksti*: build-askeleet paloina; raahaamalla muutetaan järjestystä/ajoitusta.
  - *Puhe*: aaltomuoto. Painikkeet: nauhoita (🎙), lataa tiedosto (⤒), generoi TTS muistiinpanoista (✦). Cue-merkit `[[1]]` näkyvät pystyviivoina, joihin build-askeleet kiinnittyvät.
  - *Animaatio*: efektit (sisääntulo, siirtymä, jatkuva kuten pulse/aalto). Klikkaus avaa ominaisuudet sivupaneeliin (kesto, easing, kohde).
- **Oikea ala, filmstrip.** Pikkukuvat, järjestys raahaamalla (siirtää Markdown-lohkoa).
- **Alapalkki, agentti.** Agentin tila ja muutokset diffinä; hyväksy/hylkää per dia.

### Esitystila (▶ Esitä)

Koko ruutu, näppäimet ←/→/välilyönti, `S` = puhujanäkymä (seuraava dia + muistiinpanot + kello), `A` = automaattitoisto äänen tahdissa / käsin eteneminen.

Kapealla näytöllä paneelit välilehdiksi: *Markdown | Esitys | Raidat*.

---

## 3. Markdown-syntaksi (laajennus nykyiseen Goldmark-attribuuttityyliin)

```markdown
---
title: Gemini-botti palaverissa
theme: aurora
page: 16:9
voice: fi-FI-female-1        # TTS-oletusääni
autoplay: true               # etene äänen tahdissa
---

## Miten botti **puhuu** palaverissa? {#puhe transition=fade seconds=0.6}

Puhe kokouksessa
{.kicker}

```flow
Teams-kokous -> Botin selain -> Gemini Live
```
{anim=wave}

1. Liittyy kokoukseen vieraana {.build}
2. Kuulee kokouksen äänen ja välittää sen Geminille
3. Vastaus soitetaan kokoukseen botin mikrofonina
4. Kamerakuvana animoitu hahmo, joka sykkii puheen tahdissa

![](avatar.png){anim=pulse sync=audio}

::: notes
Botti liittyy kokoukseen kuten kuka tahansa linkin saanut. [[1]]
Se kuulee äänen ja välittää sen Geminille. [[2]] ...
:::

::: audio src=puhe-3.mp3
:::
```

| Syntaksi | Merkitys | PPTX-vastine |
| --- | --- | --- |
| `{#id}` otsikolla | Pysyvä dian tunniste; agentin muokkaukset ja ääni sidotaan tähän | slide name |
| `transition=… seconds=…` | Siirtymä diaan | `PptxTransition` |
| `{.build}` listalla/lohkolla | Kohdat tulevat näkyviin yksi kerrallaan | `PptxBuildStep` |
| `anim=fade\|fly\|wave\|pulse…` | Elementin efekti | build step / (jatkuvat: vain HTML-toisto) |
| `sync=audio` | Efektin voimakkuus äänen amplitudista | ei vastinetta, jää pois |
| `::: notes` | Puhujan muistiinpanot ja TTS-käsikirjoitus | `PptxSlide.notes` |
| `[[n]]` notesissa | Cue: build-askel n laukeaa tässä kohtaa puhetta | askeleen viive |
| `::: audio src=…` | Dian ääni (nauhoitettu/ladattu/generoitu) | vaatii audio-tuen PptxWriteriin |

Periaate: kaikki mitä UI:ssa voi muuttaa, kirjoitetaan takaisin Markdowniin attribuutteina, joten Markdown pysyy ainoana totuuden lähteenä rakenteelle ja tekstille.

---

## 4. Tietomalli

```
deck.md                  ← rakenne, teksti, notes, attribuutit (totuus)
deck.evg.json            ← agentin tuottama visuaalinen kerros per #id:
                           EVG-elementit (tausta, kuvitus, efektit), keyframet
media/puhe-<id>.mp3      ← äänet, cue-ajat sidecarissa (<id>.cues.json)
```

- `MdToEvg` tuottaa dian perusrungon deterministisesti. Agentin kerros `deck.evg.json` yhdistetään siihen `#id`-avaimella, joten tekstin muokkaus ei hävitä agentin kuvitusta.
- Dia, jolta `#id` puuttuu, saa sen automaattisesti ensimmäisellä tallennuksella.
- Aikajana per dia: `{ duration, steps: [{at, target, effect, seconds, easing}] }`. `duration` = äänen pituus + 0,5 s, tai käsin asetettu.

---

## 5. Agentti

Runko otetaan EvgHarnessista (EVGPatch-opit, SSE-striimaus, valittava malli).

- **✦ Generoi** koko deckille tai valitulle dialle (`Alt+G`). Syöte: dian Markdown, teema, naapuridiat kontekstiksi. Tulos: EVGPatch-opit `deck.evg.json`:iin + ehdotetut attribuutit (`anim`, `transition`, `.build`) Markdown-diffinä + tarvittaessa notes-luonnos.
- Muutokset striimataan lavalle livenä (kuten livebuildissa); hyväksyntä per dia alapalkista.
- 🔒-lukittuja dioja agentti ei muokkaa.
- Agentti käyttää `evg_agent`-verbejä (`outline`, `query`, `measure`) tarkistaakseen, ettei teksti ylivuoda eikä laatikot mene päällekkäin, ennen kuin ehdotus näytetään.

---

## 6. Toisto ja ääni

- **Kello:** kun dialla on ääni, `<audio>.currentTime` on aikajanan kello; muuten `requestAnimationFrame`. Näin build-askeleet ja puhe pysyvät synkassa myös kelatessa.
- **Interpolointi:** uusi `EVGTimeline` (keyframet `PLAN_EVG_VIDEO.md`:n mukaan) + olemassa oleva `EVGTransition`/`EVGEasing`. Frame on puhdas funktio ajasta → sama koodi kelpaa myöhemmin MP4-vientiin.
- **`sync=audio`:** WebAudio `AnalyserNode` → RMS-arvo muuttujaksi, jota efekti (pulse, aalto) lukee.
- **Äänen lähteet:**
  1. Nauhoitus selaimessa (`MediaRecorder`), dia kerrallaan, cue-merkit painamalla `→` puhuessa.
  2. Tiedoston lataus.
  3. TTS muistiinpanoista palvelinpuolella (tiedosto tarvitaan vientiä varten; selaimen `speechSynthesis` käy vain pikaesikuunteluun). Cue-ajat TTS:n sanatason aikaleimoista.

---

## 7. Viennit

| Vienti | Sisältö | Työ |
| --- | --- | --- |
| **PDF** | Dian lopputila (kaikki build-askeleet näkyvissä); valinnainen muistiinpanosivu | Olemassa, lisätään notes-sivut |
| **PPTX** | Natiivit tekstilaatikot (nykyinen `MdToPptx`) + siirtymät + build-askeleet + notes + agentin kuvitus `PptxFromEvg`:n kautta | `MdToPptx` asettamaan `PptxTransition`/`PptxBuildStep`/`notes`; audio vaatii `p:audio`-tuen `PptxWriter`iin (media + timing), myöhempi vaihe. Jatkuvat efektit (`wave`, `sync=audio`) jäävät pois; vientiraportti (`MdDeckResult`) listaa ne |
| **HTML** | Yksi tiedosto: soitin + display listit + äänet | Uusi, jaettava esitys |
| **MP4** | Aikajana renderöitynä + ääni | `PLAN_EVG_VIDEO.md`, viimeinen vaihe |

---

## 8. Vaiheet

1. **Runko:** kaksipaneelinen sivu `MarkdownEdit`in päälle (slides-tila), filmstrip, dian valinta synkassa lähteen kanssa.
2. **Syntaksi + PPTX-pikavoitto:** `#id`, `transition`, `.build`, `::: notes` parseriin; `MdToPptx` kirjoittaa ne PPTX:ään. Tämä on hyödyllinen jo ilman soitinta.
3. **Soitin:** `EVGTimeline`, build-askeleet ja siirtymät lavalla, esitystila + puhujanäkymä.
4. **Ääni:** nauhoitus/lataus, aaltomuotoraita, cue-merkit, `sync=audio`, sitten TTS.
5. **Agentti:** EvgHarness-integraatio, `deck.evg.json`, per dia hyväksyntä, lukitus.
6. **Muokkaus lavalta:** raitojen raahaus ja ominaisuuspaneeli kirjoittavat attribuutit takaisin Markdowniin.
7. **Viennit:** HTML-soitin, PPTX-audio, MP4.

---

## 9. Avoimet kysymykset

- UI-toteutus: nykyinen tavallinen HTML + Ranger-bundle vai Rave?
- TTS-palvelu (Gemini TTS, muu) ja ajetaanko agentti/TTS paikallisen palvelimen (kuten EvgHarness `serve.mjs`) kautta.
- Kaksisuuntainen muokkaus: riittääkö, että lavalta muokataan vain tekstiä ja ajoitusta, ja visuaalinen kerros muuttuu vain agentin tai `deck.evg.json`:n kautta?
