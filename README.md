# Zebra printer compatibility for Windows on ARM and macOS

Use a Zebra GK420d as a regular printer on **macOS** and **Windows on ARM**, without
Zebra's driver and with label sizes you define yourself.

Zebra's own Windows driver only runs on x86/x64, and setting the printer up on macOS
through the CUPS web interface is cumbersome. This project avoids both. The GK420d
speaks ZPL, so the printed page is rendered as a 203 dpi image and sent to the
printer as ZPL on port 9100. No Zebra driver is needed.

## Compatibility

We have only tested this with the **Zebra GK420d**.

It may also work with other, older Zebra printers that meet the same conditions:

- the printer speaks **ZPL** (ZPL II), not only EPL
- it has a **network connection** and accepts raw print jobs on **port 9100**
- it prints at **203 dpi** with a print width of up to 104 mm

Candidates include the GK420t, GX420d/GX420t and ZD410/ZD420, all untested. Feedback
on other models is very welcome; please open an issue and mention the model and
platform.

Not supported:

- printers connected via USB only
- printers that speak EPL only (for example the LP 2844 without the "-Z")
- 300 dpi models such as the GX430t, unless adjusted. On Windows, set `dpi` in the
  configuration; on macOS, the printer description is currently fixed to 203 dpi.

## Options at a glance

| | macOS | Windows on ARM (and x64) |
|---|---|---|
| Folder | `macOS/` | `Windows/` |
| Run | `Zebra-installieren.command` | `install.cmd` |
| What it does | Creates a print queue with a custom printer description (PPD) | Installs the "Zebra bridge" as a background program and adds an IPP printer |
| Additional software | none; uses the `rastertolabel` filter that ships with macOS | `zebra-bruecke.exe` (included; source in `Quellcode/`) |
| Admin rights | yes (admin account, may ask for a password) | yes (one prompt) |
| Result | Printer "Zebra GK420d" in every print dialog | Printer "Zebra GK420d" in every print dialog |
| Label sizes | `etiketten.txt`, plus custom size in the print dialog | `etiketten.txt` |
| Remove | `lpadmin -x Zebra_GK420d` | `deinstallieren.cmd` |

File names, installer messages and the bridge's status page are in German.

## Requirements

- The printer is on the network and has a fixed IP address.
- Port 9100 on the printer is reachable from the computer.
- The printer is set to ZPL (factory default), not to EPL line mode.

## Installation on macOS

1. Copy the `macOS` folder to the Mac.
2. Double-click `Zebra-installieren.command` and enter the printer's IP address.
   The first time, right-click it and choose "Open", because the script is not signed.
3. Choose "Zebra GK420d" in the print dialog. Label sizes appear under "Paper Size",
   darkness and speed under "Printer Features".

From the Terminal, also for several printers:

```sh
./Zebra-installieren.command 192.168.1.50              # name: Zebra_GK420d
./Zebra-installieren.command 192.168.1.51 Zebra_Lager  # second printer
```

If double-clicking fails with "permission denied", the file lost its executable bit
while downloading (this happens with "Download ZIP"):

```sh
chmod +x Zebra-installieren.command
```

## Installation on Windows

1. Copy the `Windows` folder to the computer.
2. Double-click `install.cmd`, confirm the administrator prompt and enter the
   printer's IP address.
3. Choose "Zebra GK420d" in the print dialog.

The script detects ARM64 and x64 on its own. It

- copies the bridge to `C:\Program Files\ZebraBruecke`,
- starts it in the background at every system start (scheduled task `ZebraBruecke`),
- adds the printer through the IPP driver built into Windows
  (`http://127.0.0.1:631/ipp/print`).

The bridge only listens on `127.0.0.1:631` and cannot be reached from the network.

| | |
|---|---|
| Status page with test print | <http://127.0.0.1:631/> |
| Log | `C:\Program Files\ZebraBruecke\zebra-bruecke.log` |
| Remove | `deinstallieren.cmd` |

The exe files are not signed. Windows SmartScreen may warn on first start
("More info" > "Run anyway"). If you prefer not to run them, build them yourself;
see [Building from source](#building-from-source).

If the printer is not added automatically: Settings > Bluetooth & devices >
Printers & scanners > Add device > Add manually > "Select a shared printer by name",
and enter `http://127.0.0.1:631/ipp/print`.

## Label sizes

Both folders contain an `etiketten.txt`. One line per size in millimetres, optionally
followed by a name. The first line is the default size.

```text
100x150 Versandetikett
100x50
57x32 Preisetikett
```

After a change, run the installer for that platform again.

Limits: width 20 to 108 mm (printable 104 mm), height 10 to 990 mm.

## Settings

**macOS:** in the print dialog under "Printer Features" (darkness, speed, media tracking).

**Windows:** in `C:\Program Files\ZebraBruecke\zebra-bruecke.json`. Afterwards, run
`install.cmd` again or restart the computer.

| Key | Meaning | Default |
|---|---|---|
| `printer_address` | Printer IP and port | set by the installer |
| `dpi` | Printer resolution | `203` |
| `max_print_width_mm` | Printable width | `104` |
| `darkness` | Darkness 0 to 30, `-1` = printer setting | `-1` |
| `speed` | Speed 2 to 5 inches/s, `0` = printer setting | `0` |
| `media_tracking` | `gap` (labels with gaps), `mark`, `continuous`, empty = printer setting | `gap` |
| `rotate180` | Rotate the print by 180° | `false` |
| `dither` | Dither grey tones (for logos and gradients) instead of hard black/white | `false` |
| `threshold` | Percentage at which grey becomes black (without `dither`) | `50` |

## Troubleshooting

| Problem | Fix |
|---|---|
| Nothing reaches the printer | Check `ping <printer IP>` and port 9100. On Windows, open the status page and choose "Testetikett drucken" (print test label). |
| Print is offset, labels feed through | Hold the feed button until the LED flashes twice (calibration), then print again. |
| Print is upside down | macOS prints rotated by 180°, like the standard Zebra filter. On Windows, toggle `rotate180` so both behave the same. |
| Grey areas turn black (Windows) | Set `dither` to `true` or lower `threshold`. |
| Print too light or too dark | macOS: "Darkness" under Printer Features. Windows: `darkness`. |
| New size does not appear | Run the installer again after editing `etiketten.txt`. |
| Windows: bridge does not respond | Check the log. The most common cause is that port 631 is already in use. |

## How it works

**macOS:** The script builds a printer description from `zebra-gk420d.ppd.template`
and `etiketten.txt` and uses `lpadmin` to create a queue on `socket://<IP>:9100`.
The conversion to ZPL is done by the `rastertolabel` CUPS filter that ships with macOS.

**Windows:** The bridge is a small local IPP Everywhere printer. Windows sends it the
page as PWG raster through the built-in "Microsoft IPP Class Driver". The bridge
converts it into a compressed ZPL graphic label (`^GFA`) and sends it to the printer.
Ready-made ZPL is passed through unchanged.

```text
Application -> Windows print dialog -> IPP (127.0.0.1:631) -> zebra-bruecke -> ZPL -> printer:9100
```

## Building from source

The bridge is written in Go and uses only the standard library.

```sh
cd Quellcode
GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o ../Windows/zebra-bruecke-arm64.exe .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o ../Windows/zebra-bruecke-x64.exe .
```

It also runs on Linux and macOS, for example for testing:

```sh
go build -o zebra-bruecke . && ./zebra-bruecke -printer 192.168.1.50 -listen 127.0.0.1:6310
```

## License

MIT, see [LICENSE](LICENSE).

The macOS printer description (`macOS/zebra-gk420d.ppd.template`) is derived from the
Zebra ZPL driver definition in [CUPS](https://github.com/OpenPrinting/cups), which is
licensed under the Apache License 2.0. Details are in the LICENSE file.

## Disclaimer

This project is not affiliated with Zebra Technologies. "Zebra" and "GK420d" are
trademarks of their respective owner. Use at your own risk.
