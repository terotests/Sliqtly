# Sliqtly ja M-Files: suunnitelma

Tila: suunnitelma, koodia ei ole vielä. Päivitetty 2026-10-21.

Tavoite: Sliqtly-esitykset ja -dokumentit ovat M-Files-vaultissa tavallisia
objekteja (metatiedot, versiot, työnkulut, haku, oikeudet), ja niitä luodaan,
avataan ja muokataan M-Filesin sisältä tai Sliqtlystä käsin. Tallennuksen
pitää olla nopea: tavallinen muokkaus ei saa odottaa M-Filesin check-out- ja
check-in-kierrosta.

## Lyhyesti

- **Suositus:** Sliqtly-palvelin käyttää M-Filesiä tallennus-, julkaisu- ja
  metatietotaustana (vaihtoehto B), ja ohut UIX v2 -laajennus tuo Sliqtlyn
  M-Filesin komentoihin ja oikeaan paneeliin (vaihtoehto A2). Elävä
  muokkaus (OT, kursorit, AI) pysyy Sliqtlyssä, ja M-Filesiin menee versio
  kun esitys on hetken levossa tai kun käyttäjä tallentaa version.
- **Vaultissa esitys on yksi monitiedostodokumentti:** `<nimi>.md` (luettava
  teksti, M-Filesin täystekstihaku ja M-Filesin omat AI-agentit näkevät sen),
  `<nimi>.pdf` (esikatselu ilman Sliqtlyä) ja `<nimi>.sliqtly` (zip:
  teema, deck.json, kuvat ja data). Tavallinen tallennus lähettää vain
  muutaman kilotavun md-tiedoston.
- **Kokeilu ilman lisenssiä onnistuu jo:** Rangerin `gallery/mfiles` emuloi
  M-Files-asiakkaan, UIX v1/v2:n ja Vault API:n. Oikeaa vaultia varten
  kehittäjälisenssi saadaan M-Filesin kumppaniohjelman kautta; julkinen
  30 päivän kokeilu on opastettu kierros uusille käyttäjille, ei
  kehitysympäristö (ks. [Lisenssit](#lisenssit-ja-kokeilu)).

## Lähtötilanne

### M-Files

| Osa | Mitä se antaa | Sliqtlyn kannalta |
|---|---|---|
| UIX v2 (UI Extensibility Framework 2.0) | JavaScript-sovellus, joka toimii uudessa M-Files Desktopissa ja M-Files Webissä. Paketti on `.zip`/`.mfappx`, jossa `appdef.xml` ja JS-tulokohdat. API on asynkroninen (Promise). | Komennot ("Uusi Sliqtly-esitys", "Avaa Sliqtlyssä"), välilehti oikeaan paneeliin, ponnahdusikkuna. |
| Dashboardit | HTML/JS/CSS-sivu paketissa, näytetään `<iframe>`ssä. Data tulee `OnNewDashboard(dashboard)` → `dashboard.CustomData`, takaisin `shellFrame.Commands.ExecuteCommand`. Välilehti: `AddTab` → `tab.ShowDashboard(id, data)`, ponnahdus: `ShowPopupDashboard`. | Sliqtlyn katselin tai editori voi piirtää dashboardissa (EVG WebGL on tavallista JS:ää). |
| Vault API UIX v2:ssa | Objektit, versiot, tiedostot, haut, metatiedot (gRPC-viestimuoto, `@m-filescorporation/uix-vault-messages`). | Dashboardin data kulkee UIX-sovelluksen kautta vaultiin. |
| M-Files Web Service (MFWS, REST) | `X-Authentication`-token, `POST /files` (lataus), objektin luonti, `PUT …/checkedout` (out 2 / in 0), `PUT …/files/{id}/content`, ominaisuudet `…/properties`, `latest`-versio. | Sliqtly-palvelin kirjoittaa vaultiin tällä. |
| Vault Application Framework (VAF) | Palvelinpuolen .NET-sovellus vaultissa. | Ei tarvita ensimmäisissä vaiheissa. |
| Versiot ja check-out | Jokainen check-in tekee objektista uuden version, check-out lukitsee objektin yhdelle. | Ei sovi joka näppäinpainallukselle; sopii "virstanpylväsversioksi". |

Lähteet: [UIX-yleiskuvaus](https://developer.m-files.com/Frameworks/User-Interface-Extensibility-Framework),
[Dashboardit](https://developer.m-files.com/Frameworks/User-Interface-Extensibility-Framework/Reference/Overview/Dashboards),
[ShellFrame ja dashboard -esimerkki](https://developer.m-files.com/Frameworks/User-Interface-Extensibility-Framework/Reference/Samples/ShellFrameAndDashboard/),
[Sovellusten asennus](https://developer.m-files.com/Frameworks/User-Interface-Extensibility-Framework/Reference/Overview/InstallingApplications/),
[MFWS: objektien päivitys](https://developer.m-files.com/APIs/REST-API/Updating-Objects/),
[Kehittäjäportaali](https://developer.m-files.com/).

### Sliqtly (tästä repositoriosta)

- Esitys on `deck.md` + `deck.json` (nimi, teema, aihe, kansio, tiedostot) +
  `theme.css` + kansiot `media/`, `data/`, `forms/` (sama jako kuin
  `mcp-go/gitstore.go`:n snapshotissa).
- Palvelimen tallennus on rajapintojen takana: `store.Engine`
  (dokumentit, revisiot, muutosvirta) ja `store.BlobStore` (tavut
  SHA-256:n mukaan). Git-tallennus (`gitstore.go`) on jo malli sille, miten
  elävä varasto pysyy SQLitessä ja historia kirjoitetaan toiseen paikkaan
  levon jälkeen (60 s, viimeistään 10 min).
- `/api/v1` (`docs/api-v1.md`): esitysten CRUD, `ifVersion` → 409.
  OIDC ja CORS ovat jo olemassa.
- Viennit: PDF, PPTX, DOCX, HTML. MCP-palvelin AI-avustajille.

## Miten esitys on vaultissa

### Objektityyppi ja luokka

- Objektityyppi **Dokumentti** (0), luokka **Sliqtly-esitys**. Oma
  objektityyppi ei ole tarpeen: tavallisena dokumenttina esitys näkyy
  näkymissä, hauissa, työnkuluissa ja oikeuksissa kuten muutkin.
- Monitiedostodokumentti (`SingleFile` = false):

| Tiedosto | Sisältö | Miksi |
|---|---|---|
| `<nimi>.md` | `deck.md` sellaisenaan | Täystekstihaku, M-Filesin AI-agentit ja ihmiset lukevat sen ilman Sliqtlyä. Muuttuu joka versiossa, pieni. |
| `<nimi>.pdf` | Palvelimen PDF-vienti | Esikatselu M-Filesin omassa preview-paneelissa ja arkistokelpoinen muoto. Tehdään vain virstanpylväsversioon (ks. alla), ei jokaiseen. |
| `<nimi>.sliqtly` | zip: `deck.json`, `theme.css`, `media/`, `data/`, `forms/` | Kaikki mitä esityksen avaaminen tarvitsee. Lähetetään vain kun sen sisällön tiiviste muuttuu. |

Pelkkä zip yhtenä tiedostona olisi yksinkertaisin, mutta silloin vaultin haku
ja agentit eivät näkisi tekstiä, ja jokainen tallennus lähettäisi kuvat
uudelleen. Siksi teksti on omana tiedostonaan.

### Metatiedot

Pakolliset ominaisuudet luokalle (luodaan vaultin asennuksessa,
ks. vaihe 2):

| Ominaisuus | Tyyppi | Lähde |
|---|---|---|
| Nimi tai otsikko (0) | teksti | `deck.json` name |
| Sliqtly-tunniste | teksti | esityksen id, yhdistää vaultin objektin ja Sliqtlyn |
| Sliqtly-revisio | teksti | Sliqtlyn revisio tai git-commit, josta versio tehtiin |
| Pakettitiiviste | teksti | `.sliqtly`-zipin SHA-256, jolla päätetään lähetetäänkö zip |
| Diojen määrä | numero | laskettu md:stä |
| Kieli | arvolista | front matter `lang` |
| Teema | teksti | front matter / deck.json |

Valinnaiset, vaultin omiin objekteihin viittaavat ominaisuudet (Projekti,
Asiakas, Sopimus, …) valitaan Sliqtlyn metatietokortissa vaultin rakenteesta
(MFWS `/structure/classes`, `/structure/properties`). Kahteen suuntaan:

- **Vault → esitys:** front matterissa voi viitata ominaisuuksiin, esim.
  `{{mfiles.Asiakas}}`, ja arvot luetaan avattaessa. Sama mekanismi kuin
  esityksen omat data-avaimet.
- **Esitys → vault:** front matterin `mfiles:`-lohko (luokka, ominaisuudet,
  työnkulku) kirjoitetaan metatietoihin tallennettaessa. M-Filesin
  metatietokortti on silti määräävä: kortilla tehty muutos luetaan takaisin
  front matteriin, ei toisin päin ylikirjoiteta.

Työnkulku on vaultin oma (esim. Luonnos → Katselmoinnissa → Hyväksytty →
Julkaistu). Sliqtly näyttää tilan ja voi siirtää sitä
(`…/workflowstate`), mutta ei määrittele omaa.

### Versiot

Sliqtlyssä on kaksi tasoa, ja ne kuvataan M-Filesiin näin:

| Sliqtly | M-Files |
|---|---|
| Elävä muokkaus (OT, jokainen muutos) | ei näy vaultissa |
| Levon jälkeinen tilannekuva (kuten git-tallennus: 60 s levossa, viimeistään 10 min) | uusi versio: vain `.md` (ja zip jos tiiviste muuttui) |
| "Tallenna versio" tai työnkulun siirto | uusi versio + PDF + kommentti |
| Sliqtlyn versiohistoria (RangerDiff/git) | `Sliqtly-revisio`-ominaisuus kertoo, mistä kohdasta versio on |

Tallennus vaultiin, kun esitys on levossa:

1. Hae `latest`-versio ja vertaa sen `Sliqtly-revisio`ta siihen, mistä
   viimeksi kirjoitettiin.
2. Jos joku on muuttanut objektia M-Filesissä välissä (uusi tiedosto
   tai uusi md), tee siitä Sliqtlyyn kolmisuuntainen yhdistys samalla
   tavalla kuin MCP:n "hand edits since" -tieto, ja kirjoita vasta sitten.
3. `PUT …/latest/checkedout` (2), `PUT …/files/{md}/content`, tarvittaessa
   zip, ominaisuudet, `PUT …/checkedout` (0). Koko kierros on yksi
   palvelinkutsujen sarja, ja check-out on auki vain sen sekunnin.

Kukaan ei pidä objektia check-outissa sillä aikaa kun huone on auki. Jos
käyttäjä on itse ottanut objektin check-outiin M-Filesissä, Sliqtly ei
kirjoita vaan näyttää "Varattu: <nimi>" ja jatkaa omaa tallennustaan.

## Vaihtoehdot

### A. UIX v2 -laajennus

Paketti `sliqtly.mfappx`, asennetaan vaultiin M-Files Administa.

- **Komennot:** "Uusi Sliqtly-esitys" (luo objektin pohjasta),
  "Avaa Sliqtlyssä" ja "Esitä" valitulle Sliqtly-esitykselle.
- **Oikean paneelin välilehti "Sliqtly":** valitun esityksen diat
  dashboardissa (katselin).
- Kaksi tapaa toteuttaa muokkaus:

| | A1: editori paketissa | A2: ohut kehys |
|---|---|---|
| Missä editori ajetaan | Dashboardin iframe ajaa Sliqtlyn web-editorin paketista. | Dashboard avaa Sliqtly-palvelimen (yrityksen oma tai pilvi) ja antaa sille vaultin kontekstin. |
| Tallennus | Dashboard → `ExecuteCommand` → UIX-sovellus → Vault API. | Sliqtly-palvelin → MFWS (vaihtoehto B). |
| Ulkoinen palvelin | Ei tarvita. | Tarvitaan. |
| Yhteismuokkaus, AI, MCP, PDF-vienti | Ei (ne ovat palvelimessa). | Kyllä. |
| Riskit | Paketin koko, iframe-hiekkalaatikko, näppäimistön fokus, editorin päivitys = uusi paketti joka vaultiin. | Iframe-hiekkalaatikko ja kirjautuminen (yhteisön mukaan `allow-popups` puuttuu, joten OAuth-ponnahdus ei aukea), CSP, eri alkuperä. |

A1 on houkutteleva siksi, että kaikki pysyy M-Filesissä, mutta se tekee
Sliqtlystä pelkän editorin ilman sitä mikä erottaa sen. A2 kannattaa
tehdä ensin, ja kirjautuminen hoidetaan niin, että UIX-sovellus pyytää
lyhytikäisen tunnuksen Sliqtly-palvelimelta (palvelin tarkistaa sen
M-Files-istuntoa vasten) ja antaa sen dashboardille `CustomData`ssa.
Ponnahdusikkunaa ei tarvita.

### B. Sliqtly-palvelin, M-Files taustana (suositus)

Sliqtly-palvelin (`mcp-go`) saa M-Files-sovittimen:

- `mcp-go/mfiles/`: MFWS-asiakas (tunnistus, tiedostot, objektit,
  check-out/in, ominaisuudet, rakenne, haku) ja kuvaus esityksestä
  objektiin (yllä). Malli ensin, UI:sta riippumaton ja yksikkötestattu.
- Elävä varasto pysyy palvelimen omana (`store.Engine` SQLitessä), jotta
  tallennus on nopea ja OT toimii. Vault on julkaisu- ja arkistovarasto,
  johon kirjoitetaan levon jälkeen kuten `gitstore.go` kirjoittaa Gitiin.
  Vaultia ei tehdä `store.Engine`n toteutukseksi: siinä ei ole
  muutosvirtaa eikä revisioita, joita OT tarvitsee.
- Avattaessa: jos objektissa on uudempi md kuin palvelimella (joku muutti
  M-Filesissä), se tuodaan sisään ennen muokkausta.
- **Rooms ja vault:** huoneen voi sitoa vaultin näkymään tai objektiin
  (esim. Projekti), jolloin huoneen esitykset ovat sen alla.
- **Julkaisu:** katselulinkki `/s/{id}` lukee esityksen vaultista
  käyttäjän oikeuksilla, joten M-Filesin käyttöoikeudet pätevät myös
  katselussa. Julkinen linkki tehdään vain, jos vaultin työnkulun tila
  on "Julkaistu".
- **AI:** Sliqtlyn MCP-palvelin voi lukea vaultin metatietoja ja
  esityksiä sovittimen kautta. M-Filesin oma MCP on vielä tulossa
  (kesäkuun 2026 tiedote), joten tämä tuo vaultin sisällön
  AI-avustajille jo nyt, rajattuna Sliqtly-esityksiin ja niiden
  viittauksiin.
- **Kirjautuminen:** paikallinen vault: M-Files-käyttäjätunnus tai
  Windows-tunnistus MFWS:lle. Pilvi: OAuth/federoitu kirjautuminen;
  pilven MFWS-tunnistus pitää varmistaa M-Filesiltä.

### C. Työpöytäsovellus

- **Helpoin:** M-Files Desktop näyttää vaultin levynä. Kun `.sliqtly`
  tai Sliqtlyn md-tiedosto avataan sieltä, Sliqtlyn työpöytäeditori
  avaa sen ja tallennus tekee M-Filesiin version. Ei M-Files-koodia
  lainkaan. Rajoitus: M-Files Desktop on Windows-sovellus, ja Sliqtlyn
  natiivi editori on tehty ensin macOS:lle (C++/SDL2-koodi kääntyy myös
  Windowsiin, mutta pakettia ei ole).
- **REST:** työpöytäeditori puhuu Sliqtly-palvelimelle (`/api/v1`), ja
  palvelin vaultille (B). Ei uutta työtä editoriin.

C sopii yksittäiselle käyttäjälle, mutta yhteismuokkaus, julkaisu ja
metatiedot tulevat vain B:n kautta.

## Vaiheet

| Vaihe | Sisältö | Tarvitsee lisenssin |
|---|---|---|
| 0 | Emulaattoriin (Ranger `gallery/mfiles`) Sliqtly-laajennus: komennot, oikean paneelin välilehti. Emulaattoriin lisätään HTML-dashboardit (nyt kortti) ja binääritiedostot (nyt vain teksti). | Ei |
| 1 | `mcp-go/mfiles`: kuvausmalli (esitys ↔ objekti, ominaisuudet, versiot, ristiriidat) yksikkötesteineen, ja MFWS-valepalvelin testeihin (kuten `desktop/test/mock-server.mjs`). | Ei |
| 2 | Oikea vault: luokka ja ominaisuudet asennusskriptillä, tallennus levon jälkeen, avaus, ristiriidat. Mittaus: tallennuksen kesto ja vaultin versiomäärä tunnissa. | Kyllä |
| 3 | UIX v2 -paketti (A2): komennot, välilehti, kirjautuminen tunnuksella, "Uusi Sliqtly-esitys". | Kyllä |
| 4 | Metatiedot kahteen suuntaan, työnkulun tila, PDF virstanpylväsversioon, huoneen sitominen vaultin näkymään. | Kyllä |
| 5 | Tarvittaessa A1 (editori paketissa) ja työpöydän tiedostoliitos (C). | Kyllä |

Vaiheet 0–1 voidaan tehdä heti. Vaihe 2 alkaa, kun kehitysvault on
saatavilla.

## Lisenssit ja kokeilu

- **M-Filesin julkinen 30 päivän kokeilu** on pilvessä toimiva opastettu
  kierros uusille käyttäjille (mukana Aino-esikatselu). Siinä ei mainita
  ylläpito-oikeuksia eikä sovellusten asennusta, joten UIX-paketin tai
  MFWS-sovittimen kokeiluun sitä ei voi olettaa. Ei varmistettu.
- **Paikallinen asennus:** M-Files Server asennetaan Windowsille
  (.NET Framework 4.8), ja asennuksessa on arviointivaihtoehto
  näytevaultilla. Uusimmissa versioissa ensimmäinen lisenssi haetaan
  M-Files Managesta, joten ilman M-Filesin antamaa lisenssiä tätäkään ei
  välttämättä saa käyntiin. Kokeiluversio ladataan
  [Product Downloads](https://www.m-files.com/customers/product-downloads/)
  -sivulta rekisteröitymällä.
- **Kehittäjälisenssit** annetaan jälleenmyyjille ja Solution Partner
  -ohjelman jäsenille: yhteys kanavapäällikköön tai DevRel-tiimiin
  (devsupport@m-files.com). Hinta ja ehdot eivät ole julkisia.
- **Käytännössä:** vaiheet 0–1 emulaattorilla ja valepalvelimella ilman
  lisenssiä. Samalla pyydetään DevReliltä kehittäjälisenssi tai
  testivault. Emulaattoridemo on hyvä liite pyyntöön.

Lähteet: [Kehittäjäportaali](https://developer.m-files.com/),
[30 päivän kokeilu](https://community.m-files.com/blogs-65091858/b/customer-success-news/posts/free-30-day-m-files-trial---share-with-your-colleagues),
[Asennus](https://userguide.m-files.com/user-guide/latest/eng/Installing_the_software.html),
[Lisenssien hallinta](https://userguide.m-files.com/user-guide/latest/eng/License_management.html).

## Avoimet kysymykset

- UIX v2 -dashboardin iframen hiekkalaatikon tarkat attribuutit ja CSP
  M-Files Webissä ja uudessa Desktopissa: toimiiko WebGL, leikepöytä ja
  ulkoisen alkuperän sivu. Testataan vaiheessa 3.
- Saako M-Files Cloudiin asentaa omia UIX-sovelluksia ilman
  M-Filesin hyväksyntää, ja miten pilven MFWS-tunnistus toimii
  palvelulle.
- Indeksoiko M-Files `.md`-tiedostot täystekstihakuun oletuksena, vai
  pitääkö tiedostopääte lisätä asetuksiin.
- Versioiden määrä: levon jälkeinen tallennus voi tehdä kymmeniä
  versioita päivässä. Jos se on liikaa, vaultiin kirjoitetaan vain
  "Tallenna versio" ja työnkulun siirto, ja levon jälkeinen tilannekuva
  pysyy Sliqtlyn omassa historiassa.
- Paketoitavan editorikoodin lisenssiehdot asiakkaan vaultiin
  jaettaessa (A1).
