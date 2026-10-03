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
| Code editor + Markdown-kieli + esikatselu yhdellä EVG-canvasilla | `Ranger/gallery/r5` (`R5App`, `R5MdLanguage`, `R5Merge`), `gallery/datagrid/src/script/ScriptEditor.rgr` | Suora pohja: editori, slides-näkymä, kaksisuuntainen patch, host-rajapinta |
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

- **Vasen, Markdown.** Rangerin code editor (`ScriptEditor`, `gallery/datagrid`) Markdown-tokenisoijalla (`R5MdLanguage`), kuten r5:ssä: syntaksiväritys, minimap, undo, suggest (Ctrl+Space, tähän attribuutit `transition=`, `anim=` jne.). Kursori seuraa valittua diaa ja päinvastoin (dian vaihto esityksessä vierittää lähteen `MdSrcMap`illa). Gutterissa ikonit: 🔊 = dialla on ääni, ✦ = agentin muokkaama, 🔒 = lukittu (agentti ei koske).
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

## 2b. Toteutus: EVG renderöi, HTML liimaa

Pohjana `gallery/r5`: sama rakenne on siellä jo toiminnassa (code editor + MarkdownWeb + slides yhdellä canvasilla, `MdEditController` omistaa tekstin ja editorin puskuri on sen näkymä).

**EVG:llä piirretään (yksi WebGL-canvas, `evg-webgl.js`):**
- Markdown-editori (`ScriptEditor` + `R5MdLanguage`, laajennettuna uusilla attribuuteilla ja `::: notes`/`::: audio`-lohkoilla)
- Lava: dia muokkaus- ja toistotilassa
- Raidat: build-palat, aaltomuoto, efektipalat, toistopää, cue-viivat
- Filmstrip-pikkukuvat
- Esitystila ja puhujanäkymä

**HTML:llä (liima ja tavalliset UI-elementit):**
- Yläpalkki: teema, kuvasuhde, Generoi, Esitä, Vie-valikko
- Ominaisuuspaneeli (kesto, easing, siirtymä): `<input>`, `<select>`
- Agenttipaneeli ja dialogit (TTS-ääni, vientiasetukset)
- Selaimen rajapinnat: `<audio>`, `MediaRecorder`, `AnalyserNode`, tiedostonvalitsin, lataukset, `fetch`/SSE agentille
- Piilotettu tekstikenttä näppäimistösyötteelle (kuten r5:n `main.js`)

**Rajapinta Ranger-sovelluksen ja HTML:n välillä** r5:n mallin mukaan: sovellusluokka (`PresApp`) ottaa kutsut (`pointerDown`, `key`, `text`, `setSource`, `setTime`, `setAudioLevel`, `setAudioClip`, `applyPatch`) ja palauttaa JSONia (`frame()`, `paneRectsJson()`, `timelineJson()`) sekä `revision()`-laskurin. Selaimelle suunnatut pyynnöt (nauhoita, lataa tiedosto, soita, kysy agentilta) kulkevat `takeRequest()`-merkkijonoina, joten sama luokka ajetaan Nodessa testeissä ilman selainta. HTML-elementit asemoidaan canvasin päälle `paneRectsJson()`:n antamiin kohtiin.

Äänen kello pysyy HTML-puolella (`<audio>.currentTime`), ja se syötetään joka framella `setTime`/`setAudioLevel`-kutsuilla. Ranger-puoli ei tiedä äänestä muuta kuin ajan, tason ja clipin pituuden.

**Repo:**
```
EVGPresentation/
  src/PresApp.rgr        sovellus: paneelit, syöte, tila
  src/PresTimeline.rgr   aikajana (siirretään Rangerin lib/evg:hen kun vakiintuu)
  src/PresTracks.rgr     raitojen piirto ja muokkaus
  src/PresDeck.rgr       deck.md + deck.evg.json + media yhdistäminen
  web/index.html         canvas + HTML-kontrollit
  web/main.js            WebGL-frame, syöte, audio, agentti-SSE
  web/pres.css
  scripts/setup.mjs      Ranger (+ EvgHarness) .deps/-hakemistoon
```

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

## Miten botti **puhuu** palaverissa? {#puhe transition=fade seconds=0.6 fx=starfield fx-density=1.6}

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
| `fx=starfield fx-<param>=…` | EVG:n pintaefekti dian tai elementin taustaksi (ks. 5b) | ei vastinetta; PPTX:ään voi viedä pysäytyskuvan taustakuvana |
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

## 5b. Kuvitus: liitä leikepöydältä ja EVG-efektit

**Liitä leikepöydältä (Ctrl+V lavalla tai editorissa):**
- Kuva (`image/png`, `image/jpeg`) → tallennetaan `media/paste-<n>.png` ja lisätään valitulle dialle `![](media/paste-<n>.png)` kursorin tai valitun lohkon kohdalle.
- SVG-teksti → tallennetaan `media/*.svg` ja lisätään kuvana.
- EVG JSON (esim. EvgHarnessista kopioitu) → lisätään dian kuvituskerrokseen `deck.evg.json`:iin.
- Teksti/HTML → nykyinen r5:n `paste`-käsittely (`gallery/r5/web/main.js`) sellaisenaan.

Liittäminen tapahtuu HTML-puolella (`paste`-tapahtuma, `clipboardData.items`), ja tiedosto viedään Ranger-sovellukselle `attachImage(name, bytes)`-kutsulla, kuten fontit nykyään.

**EVG:n pintaefektit** (`lib/evg/PLAN_EFFECTS.md`) ovat CSS-ominaisuuksia, joten ne sopivat suoraan teeman `style.css`:ään tai dian attribuutteihin:

```css
.slide#puhe {
  evg-surface-effect: starfield;
  evg-effect-on: always;
  evg-fx-density: 1.6;
  evg-fx-hue: 228;
}
```

Markdownissa lyhenne `{fx=starfield fx-density=1.6}` kirjoitetaan samoiksi ominaisuuksiksi. Käytettävissä: `starfield`, `plasma-wave`, `smoke`, `ambient-light` (taustat), `liquid-glass`, `drops` (sade ikkunassa: pisarat yhdistyvät ja valuvat), `raindrops2` (sama, mutta valuva pisara jättää vesiviivan ja jää sen päähän odottamaan), `bubbles` (pyöreät pisarat, ennen `raindrop`) (lasi elementin päällä), `ripple` (suodin). UI:ssa efektit valitaan animaatioraidan valikosta esikatselukuvien kanssa, ja parametrit säädetään ominaisuuspaneelissa. Aikajana voi animoida efektin parametreja (esim. tähtien tiheys kasvaa dian aikana) samoilla keyframeilla kuin muutkin ominaisuudet.

---

## 5c. Determinismi ja videokaappaus

Tavoite: sama esitys ja sama aika t → sama kuva, jotta video voidaan kaapata kuva kerrallaan eikä reaaliajassa.

Nykytila:
- **Efektishaderit** ovat jo puhtaita funktioita: ne lukevat vain `uTime`, parametrit ja tapahtumalistan, ja satunnaisuus tulee hash-funktioista (`fxHash21`), ei `Math.random`ista.
- **`evg-fx.js`-ajuri** kasvattaa kelloa `clock += dt` joka framella. Tarvitaan `seek(t)`, joka asettaa kellon suoraan.
- **`EVGTransition`** etenee `advance(el, dtMs)`-askelin eikä sillä ole omaa kelloa. Kiinteällä askeleella (1000/fps) tulos toistuu, mutta kelaus vaatii ajon alusta. `EVGTimeline` lasketaan suoraan ajasta t (keyframe-interpolointi), joten esityksen animaatiot eivät käytä `advance`-askeleita.
- **Painalluksesta laukeavat efektit** (`ripple`) saavat esityksessä tapahtumansa aikajanalta eivätkä osoittimesta.

Kaappaus:
1. Toistosilmukka ajetaan virtuaalikellolla `t = frame / fps` eikä `requestAnimationFrame`illa.
2. Jokaiselle framelle: `setTime(t)`, efektiajurille `seek(t)`, piirto, `readPixels` / `VideoFrame(canvas)`.
3. Koodaus selaimessa WebCodecsin `VideoEncoder`illa ja MP4-muxerilla (`gallery/evg_video/PLAN_EVG_VIDEO.md`), tai vaihtoehtoisesti PNG-sarja headless Chromesta ja ffmpeg.
4. Ääni miksataan klipeistä niiden tunnetuilla alkuajoilla.

GPU:t voivat erota liukulukutarkkuudessa, joten tavutarkka toistettavuus luvataan saman koneen sisällä. CI:ssä käytetään headless Chromea SwiftShaderilla, jolloin tulos on sama joka ajolla. Testi: renderöi frame N kahdesti (myös kelauksen jälkeen) ja vertaa tiivisteitä.

---

## 6. Toisto ja ääni

- **Kello:** kun dialla on ääni, `<audio>.currentTime` on aikajanan kello; muuten `requestAnimationFrame`. Näin build-askeleet ja puhe pysyvät synkassa myös kelatessa.
- **Interpolointi:** uusi `EVGTimeline` (keyframet `PLAN_EVG_VIDEO.md`:n mukaan) + olemassa oleva `EVGTransition`/`EVGEasing`. Frame on puhdas funktio ajasta → sama koodi kelpaa myöhemmin MP4-vientiin.
- **`sync=audio`:** WebAudio `AnalyserNode` → RMS-arvo muuttujaksi, jota efekti (pulse, aalto) lukee.
- **Äänen lähteet:**
  1. Nauhoitus selaimessa (`MediaRecorder`), dia kerrallaan, cue-merkit painamalla `→` puhuessa.
  2. Tiedoston lataus.
  3. TTS muistiinpanoista (ei vaiheessa 1). Ks. 6b.

### 6b. TTS testaukseen

Palvelinpuolen TTS tarvitaan, koska vientiin tarvitaan äänitiedosto; selaimen `speechSynthesis` käy vain pikaesikuunteluun (ääntä ei saa talteen, ja äänet vaihtelevat koneittain).

| Vaihtoehto | Suomi | Hinta | Huom. |
| --- | --- | --- | --- |
| **Piper** (paikallinen, `pip install piper-tts`) | `fi_FI-harri-medium` | ilmainen | Ei avainta eikä verkkoa ajon aikana; sama syöte → sama tiedosto, joten sopii testeihin ja CI:hin. Äänimalli ladataan kerran Hugging Facesta. |
| **Gemini TTS** (API) | kyllä | ilmainen kehitystaso rajoituksin | EvgHarness käyttää jo Gemini-avainta, joten yksi avain riittää. |
| Google Cloud TTS | fi-FI (Standard/WaveNet) | kuukausittainen ilmaiskiintiö | Vaatii GCP-projektin. |
| Azure Speech | fi-FI-NooraNeural, HarriNeural | ilmaistaso | Antaa sanatason aikaleimat. |
| espeak-ng | kyllä | ilmainen | Robottimainen, mutta asentuu apt:lla; kelpaa savutestiin. |

Suositus: Piper oletukseksi (testit, offline), Gemini TTS valinnaiseksi laadukkaammaksi ääneksi. Palvelinpuolen rajapinta on yksi: `POST /tts {text, voice} → wav`.

Cue-ajat ilman palvelun aikaleimoja: muistiinpanot pilkotaan `[[n]]`-merkkien kohdalta, jokainen pala syntetisoidaan erikseen ja palat liitetään yhteen. Cue n = palojen 1..n yhteispituus. Toimii kaikilla palveluilla samalla tavalla.

(Tässä ympäristössä Piperin äänimallin lataus Hugging Facesta estyi verkkorajoituksen takia, joten sitä ei ajettu täällä.)

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

Vaihe 1 = kohdat 1–4 (runko, syntaksi, soitin, kuvitus ja efektit). Ääni ja TTS tulevat sen jälkeen.

1. **Runko:** `PresApp` r5:n pohjalta: code editor + lava + filmstrip yhdellä canvasilla, HTML-yläpalkki, dian valinta synkassa lähteen kanssa.
2. **Syntaksi + PPTX-pikavoitto:** `#id`, `transition`, `.build`, `::: notes` parseriin; `MdToPptx` kirjoittaa ne PPTX:ään. Tämä on hyödyllinen jo ilman soitinta.
3. **Soitin:** `EVGTimeline`, build-askeleet ja siirtymät lavalla, esitystila + puhujanäkymä.
4. **Kuvitus:** liitä leikepöydältä, `fx=`-efektit ja niiden valikko, efektiajurin `seek(t)`.
5. **Ääni:** nauhoitus/lataus, aaltomuotoraita, cue-merkit, `sync=audio`, sitten TTS (Piper, valinnaisesti Gemini).
6. **Agentti:** EvgHarness-integraatio, `deck.evg.json`, per dia hyväksyntä, lukitus.
7. **Muokkaus lavalta:** raitojen raahaus ja ominaisuuspaneeli kirjoittavat attribuutit takaisin Markdowniin.
8. **Viennit:** HTML-soitin, PPTX-audio, MP4 deterministisellä kaappauksella (5c).

---

## 8b. Vaiheen 1 tila

Tehty (`README.md` kertoo käytön):
- Runko: `PresApp` (code editor + lava + raidat + filmstrip yhdellä EVG-canvasilla), HTML-yläpalkki, paikallinen palvelin (`npm start`).
- Syntaksi: `{#id transition= seconds= duration= fx= fx-*=}` otsikossa, `{.build}` ja `{anim= seconds=}` lohkon perässä, `::: notes` ja `::: audio`, front matter -oletukset.
- PPTX: muistiinpanot (cue-merkit poistettuina), siirtymät ja build-askel per listan kappale. Tehty EVGPresentationissa (`PresApp.dressSlide`); `MdToPptx` Rangerissa ei muuttunut.
- Soitin: `PresTimeline` laskee kuvan suoraan ajasta; toisto, raidan scrubbaus, esitystila, puhujanäkymä (S), automaattinen eteneminen (A).
- Kuvitus: kuvan liittäminen leikepöydältä ja raahaus canvasille; `fx=` ja `fx-*` diakohtaisesti; PDF-vienti kuvineen.
- Efektien aika: isäntä asettaa `effects[].time` suoraan deckin ajasta, joten `evg-fx.js`:n `seek(t)`-muutosta ei tarvittu.

Jäi vaiheesta 1 pois:
- Efektivalikko esikatselukuvineen (efekti valitaan nyt kirjoittamalla `fx=`).
- SVG:n ja EVG JSONin liittäminen (vain PNG/JPEG/GIF/WebP).
- Raidoilta muokkaus (raahaus kirjoittaa attribuutit): vaihe 7.

---

## 9. Avoimet kysymykset

- Kaksisuuntainen muokkaus: riittääkö, että lavalta muokataan vain tekstiä ja ajoitusta, ja visuaalinen kerros muuttuu vain agentin tai `deck.evg.json`:n kautta?
