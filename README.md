# Zebra GK420d ohne Herstellertreiber

Den Zebra GK420d unter **macOS** und **Windows on ARM** als normalen Drucker nutzen,
mit frei definierbaren Etikettenformaten.

Zebras eigener Windows-Treiber läuft nur auf x86/x64, und unter macOS ist die
Einrichtung über die CUPS-Weboberfläche umständlich. Dieses Projekt umgeht beides:
Der GK420d spricht ZPL, also wird die Druckseite mit 203 dpi als Bild gerendert und
als ZPL an Port 9100 des Druckers geschickt. Ein Zebra-Treiber wird nicht gebraucht.

> **Teststand:** Bisher nur gegen einen simulierten Drucker geprüft, nicht auf einem
> echten GK420d, einem echten Mac oder unter Windows. Details unter [Teststand](#teststand).
> Rückmeldungen aus dem echten Betrieb sind willkommen.

## Die Optionen im Überblick

| | macOS | Windows on ARM (und x64) |
|---|---|---|
| Ordner | `macOS/` | `Windows/` |
| Start | `Zebra-installieren.command` | `install.cmd` |
| Was passiert | Legt eine Druckerwarteschlange mit eigener Druckerbeschreibung (PPD) an | Installiert die „Zebra-Brücke“ als Hintergrundprogramm und legt einen IPP-Drucker an |
| Zusätzliche Software | keine, nutzt den in macOS enthaltenen Filter `rastertolabel` | `zebra-bruecke.exe` (liegt bei, Quellcode in `Quellcode/`) |
| Administratorrechte | ja (Admin-Konto, ggf. Passwortabfrage) | ja (einmalige Abfrage) |
| Ergebnis | Drucker „Zebra GK420d“ in jedem Druckdialog | Drucker „Zebra GK420d“ in jedem Druckdialog |
| Formate | `etiketten.txt`, zusätzlich freie Größe im Druckdialog | `etiketten.txt` |
| Entfernen | `lpadmin -x Zebra_GK420d` | `deinstallieren.cmd` |

## Voraussetzungen

- Der Drucker hängt im Netzwerk und hat eine feste IP-Adresse.
- Port 9100 des Druckers ist vom Rechner aus erreichbar.
- Der Drucker steht auf ZPL (Auslieferungszustand), nicht auf EPL-Zeilenmodus.

USB-Anschluss wird nicht unterstützt.

## Installation unter macOS

1. Ordner `macOS` auf den Mac kopieren.
2. `Zebra-installieren.command` doppelklicken und die IP-Adresse des Druckers eingeben.
   Beim ersten Mal über Rechtsklick > „Öffnen“ starten, weil das Skript nicht signiert ist.
3. Im Druckdialog „Zebra GK420d“ wählen. Die Formate stehen unter „Papierformat“,
   Schwärzung und Geschwindigkeit unter „Druckeroptionen“.

Aufruf im Terminal, auch für mehrere Drucker:

```sh
./Zebra-installieren.command 192.168.1.50              # Name: Zebra_GK420d
./Zebra-installieren.command 192.168.1.51 Zebra_Lager  # zweiter Drucker
```

Falls der Doppelklick mit „keine Berechtigung“ scheitert, hat die Datei beim Herunterladen
ihr Ausführungsrecht verloren:

```sh
chmod +x Zebra-installieren.command
```

## Installation unter Windows

1. Ordner `Windows` auf den Rechner kopieren.
2. `install.cmd` doppelklicken, die Administratorabfrage bestätigen und die IP-Adresse eingeben.
3. Im Druckdialog „Zebra GK420d“ wählen.

Das Skript erkennt ARM64 und x64 selbst. Es

- kopiert die Brücke nach `C:\Program Files\ZebraBruecke`,
- startet sie bei jedem Systemstart im Hintergrund (geplante Aufgabe `ZebraBruecke`),
- legt den Drucker über den in Windows eingebauten IPP-Treiber an
  (`http://127.0.0.1:631/ipp/print`).

Die Brücke lauscht nur lokal auf `127.0.0.1:631` und ist aus dem Netzwerk nicht erreichbar.

| | |
|---|---|
| Statusseite mit Testdruck | <http://127.0.0.1:631/> |
| Protokoll | `C:\Program Files\ZebraBruecke\zebra-bruecke.log` |
| Entfernen | `deinstallieren.cmd` |

Die exe-Dateien sind nicht signiert. Windows SmartScreen kann beim ersten Start warnen
(„Weitere Informationen“ > „Trotzdem ausführen“). Wer das nicht möchte, baut die exe
selbst, siehe [Selbst bauen](#selbst-bauen).

Falls der Drucker nicht automatisch angelegt wird: Einstellungen > Bluetooth und Geräte >
Drucker und Scanner > Gerät hinzufügen > Manuell hinzufügen > „Freigegebenen Drucker über
den Namen auswählen“ und `http://127.0.0.1:631/ipp/print` eintragen.

## Etikettenformate

In beiden Ordnern liegt eine `etiketten.txt`. Eine Zeile pro Format in Millimetern,
optional mit Namen. Die erste Zeile ist das Standardformat.

```text
100x150 Versandetikett
100x50
57x32 Preisetikett
```

Nach einer Änderung das Installationsskript der jeweiligen Plattform erneut starten.

Grenzen: Breite 20 bis 108 mm (bedruckbar 104 mm), Höhe 10 bis 990 mm.

## Einstellungen

**macOS:** im Druckdialog unter „Druckeroptionen“ (Schwärzung, Geschwindigkeit,
Etikettenerkennung).

**Windows:** in `C:\Program Files\ZebraBruecke\zebra-bruecke.json`. Danach `install.cmd`
erneut ausführen oder den Rechner neu starten.

| Schlüssel | Bedeutung | Standard |
|---|---|---|
| `printer_address` | IP und Port des Druckers | vom Installer gesetzt |
| `darkness` | Schwärzung 0 bis 30, `-1` = Druckereinstellung | `-1` |
| `speed` | Geschwindigkeit 2 bis 5 Zoll/s, `0` = Druckereinstellung | `0` |
| `media_tracking` | `gap` (Etiketten mit Lücke), `mark`, `continuous`, leer = Druckereinstellung | `gap` |
| `rotate180` | Druckbild um 180° drehen | `false` |
| `dither` | Graustufen rastern (für Logos und Verläufe) statt hart schwarz/weiß | `false` |
| `threshold` | Schwelle in Prozent, ab der Grau schwarz wird (ohne `dither`) | `50` |

## Fehlersuche

| Problem | Abhilfe |
|---|---|
| Es kommt nichts am Drucker an | `ping <Drucker-IP>` und Port 9100 prüfen. Unter Windows die Statusseite öffnen und „Testetikett drucken“ wählen. |
| Druckbild verschoben, Etikett läuft durch | Vorschubtaste gedrückt halten, bis die LED zweimal blinkt (Kalibrierung), dann erneut drucken. |
| Druck steht auf dem Kopf | macOS druckt wie der Standard-Zebra-Filter um 180° gedreht. Unter Windows `rotate180` umstellen, damit beide gleich laufen. |
| Graue Flächen werden schwarz (Windows) | `dither` auf `true` setzen oder `threshold` senken. |
| Druck zu hell oder zu dunkel | macOS: „Darkness“ in den Druckeroptionen. Windows: `darkness`. |
| Neues Format erscheint nicht | Installationsskript nach dem Ändern der `etiketten.txt` erneut starten. |
| Windows: Brücke antwortet nicht | Protokoll ansehen. Häufigste Ursache: Port 631 ist schon belegt. |

## So funktioniert es

**macOS:** Das Skript erzeugt aus `zebra-gk420d.ppd.template` und `etiketten.txt` eine
Druckerbeschreibung und richtet damit per `lpadmin` eine Warteschlange auf
`socket://<IP>:9100` ein. Die Umwandlung nach ZPL übernimmt der mit macOS gelieferte
CUPS-Filter `rastertolabel`.

**Windows:** Die Brücke ist ein kleiner lokaler IPP-Everywhere-Drucker. Windows schickt
ihr die Seite als PWG-Raster über den eingebauten „Microsoft IPP Class Driver“, die Brücke
wandelt sie in ein ZPL-Grafiketikett (`^GFA`, komprimiert) um und sendet es an den Drucker.
Fertiges ZPL wird unverändert durchgereicht.

```text
Anwendung -> Windows-Druckdialog -> IPP (127.0.0.1:631) -> zebra-bruecke -> ZPL -> Drucker:9100
```

Andere ZPL-Drucker mit 203 dpi sollten nach demselben Prinzip funktionieren, sind aber
nicht geprüft. Für andere Auflösungen `dpi` und `max_print_width_mm` in der
Konfiguration anpassen.

## Selbst bauen

Die Brücke ist in Go geschrieben und nutzt nur die Standardbibliothek.

```sh
cd Quellcode
GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o ../Windows/zebra-bruecke-arm64.exe .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o ../Windows/zebra-bruecke-x64.exe .
```

Sie läuft auch unter Linux und macOS, etwa zum Testen:

```sh
go build -o zebra-bruecke . && ./zebra-bruecke -printer 192.168.1.50 -listen 127.0.0.1:6310
```

## Teststand

Geprüft in einer Linux-Testumgebung gegen einen simulierten Drucker:

- macOS-Druckerbeschreibung mit dem CUPS-Filter `rastertolabel`
- Brücke mit CUPS als IPP-Client: Standardformat, weitere Formate, freie Größe, Kopien,
  1-Bit- und Graustufen-Raster
- Die erzeugten ZPL-Daten wurden zurückdekodiert und mit der Vorlage verglichen.

Noch nicht geprüft:

- Druck auf einem echten GK420d
- `Zebra-installieren.command` auf einem echten Mac
- `install.ps1` und das Zusammenspiel mit dem Windows-IPP-Treiber, insbesondere die
  Anzeige eigener Formate im Druckdialog

## Hinweise

Dieses Projekt steht in keiner Verbindung zu Zebra Technologies. „Zebra“ und „GK420d“
sind Marken ihres Inhabers. Nutzung auf eigene Verantwortung.
