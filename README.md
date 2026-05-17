# Meidan

Kleiner Prototyp fuer einen Weinen-Alarm:

- ESP32 misst ein Geraeuschsignal und sendet Events plus Lautstaerke-Werte per HTTP.
- Go-Server speichert die letzten Events und kann Audio optional mit Gemini klassifizieren.
- Flutter-App pollt den Server und zeigt Weinen- sowie Lautstaerke-Warnungen.

## Server starten

```sh
cd server
go run .
```

Optional mit Gemini:

```sh
cd server
GEMINI_API_KEY="dein-key" go run .
```

Bei temporaeren Gemini-Fehlern wie `503 Service Unavailable` versucht der Server automatisch kurz erneut und wechselt danach auf Fallback-Modelle. Aendern geht so:

```sh
GEMINI_API_KEY="dein-key" GEMINI_MODEL="gemini-2.5-flash" GEMINI_FALLBACK_MODELS="gemini-2.0-flash,gemini-2.5-flash-lite" go run .
```

Standard-Port ist `:8080`. Aendern geht mit:

```sh
ADDR=":9000" go run .
```

Die Warnschwelle fuer sehr laute Messwerte ist standardmaessig `1000`. Aendern geht so:

```sh
LOUD_VOLUME_THRESHOLD="1400" go run .
```

## Schnell testen

```sh
curl -X POST http://localhost:8080/api/events \
  -H "Content-Type: application/json" \
  -d '{"deviceId":"demo","volume":82}'
```

Events ansehen:

```sh
curl http://localhost:8080/api/events
```

Lautstaerke messen:

```sh
curl -X POST http://localhost:8080/api/volume \
  -H "Content-Type: application/json" \
  -d '{"deviceId":"demo","volume":350}'
```

Sehr laute Warnung ausloesen:

```sh
curl -X POST http://localhost:8080/api/volume \
  -H "Content-Type: application/json" \
  -d '{"deviceId":"demo","volume":1400}'
```

Messwerte ansehen:

```sh
curl http://localhost:8080/api/volume
```

Audio fuer Gemini kann als Base64 gesendet werden:

```json
{
  "deviceId": "esp32-audio",
  "mimeType": "audio/wav",
  "audioBase64": "..."
}
```

Ohne `GEMINI_API_KEY` faellt der Server automatisch auf die lokale Lautstaerke-Regel zurueck.

## Automatische Tests

```sh
cd server
go test ./...
```

## Flutter-App

Die App ist bewusst klein gehalten und enthaelt nur `pubspec.yaml` und `lib/main.dart`.

```sh
cd app
flutter pub get
flutter run -d web-server --web-port=8081 --dart-define=SERVER_URL=http://localhost:8080
```

Dann oeffnen:

```sh
http://localhost:8081
```

Fuer Warn-Toene in der UI einmal `Ton ein` druecken. Danach spielt die App bei `Weinen erkannt` die Datei `Assets/we.mp3` und bei `Sehr laut` die Datei `Assets/auto.mp3`.

Android/iOS-Ordner sind absichtlich nicht enthalten, damit das Projekt klein bleibt. Falls du spaeter eine echte Handy-App bauen willst:

```sh
cd app
flutter create . --platforms=android,ios
flutter run --dart-define=SERVER_URL=http://10.0.2.2:8080
```

Auf einem echten Handy muss statt `10.0.2.2` die lokale IP deines Rechners genutzt werden, z. B. `http://192.168.178.20:8080`.

## ESP32

Oeffne `esp32/baby_cry_sensor/baby_cry_sensor.ino` in der Arduino IDE und passe an:

- `WIFI_SSID`
- `WIFI_PASSWORD`
- `EVENT_URL`
- `VOLUME_URL`
- `SOUND_PIN`
- `CRY_THRESHOLD`

Der Sketch sendet Weinen-Events an `/api/events` und Pegelwerte an `/api/volume`. Wenn ein Pegel den Server-Schwellwert `LOUD_VOLUME_THRESHOLD` ueberschreitet, erscheint automatisch eine Lautstaerke-Warnung in der UI.
