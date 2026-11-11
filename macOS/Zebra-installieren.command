#!/bin/bash
# Richtet den Zebra GK420d (Netzwerk, ZPL) als normalen macOS-Drucker ein.
# Aufruf: Doppelklick, oder im Terminal:  ./Zebra-installieren.command [IP-ADRESSE] [DRUCKERNAME]
set -euo pipefail
cd "$(dirname "$0")"

QUEUE="${2:-Zebra_GK420d}"
SIZES="etiketten.txt"
TEMPLATE="zebra-gk420d.ppd.template"
FILTER="/usr/libexec/cups/filter/rastertolabel"

[ -f "$SIZES" ] && [ -f "$TEMPLATE" ] || { echo "FEHLER: $SIZES oder $TEMPLATE fehlt neben diesem Skript."; exit 1; }
[ -x "$FILTER" ] || [ "$(uname)" != "Darwin" ] || { echo "FEHLER: $FILTER nicht gefunden - diese macOS-Version liefert den Label-Filter nicht mehr mit."; exit 1; }

IP="${1:-}"
if [ -z "$IP" ]; then
  # bei erneutem Lauf die bisherige Adresse vorschlagen
  OLD="$(lpstat -v "$QUEUE" 2>/dev/null | sed -n 's|.*socket://\([^:/]*\).*|\1|p' || true)"
  read -r -p "IP-Adresse des Druckers${OLD:+ [$OLD]}: " IP
  IP="${IP:-$OLD}"
fi
[ -n "$IP" ] || { echo "FEHLER: keine IP-Adresse angegeben."; exit 1; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/zebra.XXXXXX")"
PPD="$TMP/zebra-gk420d.ppd"
BLOCK="$TMP/sizes"

# Groessenliste -> PPD-Eintraege (1 mm = 72/25.4 Punkt)
awk '
  /^[ \t]*#/ || /^[ \t]*$/ { next }
  {
    spec=$1; gsub(/,/, ".", spec); n=split(tolower(spec), d, "x")
    if (n != 2 || d[1]+0 <= 0 || d[2]+0 <= 0) { printf "FEHLER in etiketten.txt: \"%s\"\n", $0 > "/dev/stderr"; bad=1; next }
    w=d[1]+0; h=d[2]+0
    if (w > 108 || h > 990) { printf "FEHLER: %s ist zu gross (max. 108 x 990 mm)\n", spec > "/dev/stderr"; bad=1; next }
    id=w "x" h "mm"; if (id ~ /\./) { id="L" w "x" h; gsub(/\./, "_", id) }
    label=w " x " h " mm"
    if (NF > 1) { name=$0; sub(/^[ \t]*[^ \t]+[ \t]+/, "", name); label=label " (" name ")" }
    c++; ids[c]=id; labels[c]=label
    pw[c]=sprintf("%.2f", w*72/25.4); ph[c]=sprintf("%.2f", h*72/25.4)
  }
  END {
    if (bad || c == 0) { if (!bad) print "FEHLER: etiketten.txt enthaelt kein Format" > "/dev/stderr"; exit 1 }
    for (k=1; k<=2; k++) {
      o=(k==1) ? "PageSize" : "PageRegion"
      printf "*OpenUI *%s/Media Size: PickOne\n*OrderDependency: 10 AnySetup *%s\n*Default%s: %s\n", o, o, o, ids[1]
      for (i=1; i<=c; i++) printf "*%s %s/%s: \"<</PageSize[%s %s]/ImagingBBox null>>setpagedevice\"\n", o, ids[i], labels[i], pw[i], ph[i]
      printf "*CloseUI: *%s\n", o
    }
    printf "*DefaultImageableArea: %s\n", ids[1]
    for (i=1; i<=c; i++) printf "*ImageableArea %s/%s: \"0 0 %s %s\"\n", ids[i], labels[i], pw[i], ph[i]
    printf "*DefaultPaperDimension: %s\n", ids[1]
    for (i=1; i<=c; i++) printf "*PaperDimension %s/%s: \"%s %s\"\n", ids[i], labels[i], pw[i], ph[i]
  }' "$SIZES" > "$BLOCK"

awk -v f="$BLOCK" '$0=="@@SIZES@@" { while ((getline l < f) > 0) print l; next } { print }' "$TEMPLATE" > "$PPD"

echo "Richte Drucker \"$QUEUE\" an $IP ein ..."
if ! lpadmin -p "$QUEUE" -E -v "socket://$IP:9100" -P "$PPD" -D "Zebra GK420d" -o printer-is-shared=false 2>/dev/null; then
  echo "Administratorrechte noetig - bitte Passwort eingeben:"
  sudo lpadmin -p "$QUEUE" -E -v "socket://$IP:9100" -P "$PPD" -D "Zebra GK420d" -o printer-is-shared=false
fi
rm -rf "$TMP"

echo
echo "Fertig. Der Drucker \"Zebra GK420d\" steht jetzt in allen Druckdialogen bereit."
echo "Formate: $(lpoptions -p "$QUEUE" -l | sed -n 's/^PageSize[^:]*: //p')"
echo "Weitere Formate: etiketten.txt ergaenzen und dieses Skript erneut starten."
echo "Testdruck:  lp -d $QUEUE /pfad/zur/datei.pdf"
