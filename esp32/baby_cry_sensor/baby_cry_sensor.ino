#include <HTTPClient.h>
#include <WiFi.h>
#include "mbedtls/base64.h"

const char* WIFI_SSID =  "Jowan";
const char* WIFI_PASSWORD = "jowan112";
const char* SERVER_HOST = "172.20.10.6";
const uint16_t SERVER_PORT = 8080;
const char* DEVICE_ID = "esp32";

const int SOUND_PIN = 34;
const int CRY_THRESHOLD = 80;
const int CRY_HITS_REQUIRED = 2;
const unsigned long SAMPLE_WINDOW_MS = 120;
const unsigned long EVENT_INTERVAL_MS = 10000;
const unsigned long VOLUME_INTERVAL_MS = 3000;
const bool SEND_AUDIO_TO_AI = true;
const int AUDIO_SAMPLE_RATE = 8000;
const int AUDIO_SECONDS = 2;
const int AUDIO_SAMPLES = AUDIO_SAMPLE_RATE * AUDIO_SECONDS;
const int WAV_HEADER_BYTES = 44;
const float AUDIO_NORMALIZE_TARGET = 110.0;
const float AUDIO_MAX_GAIN = 12.0;

unsigned long lastEventSend = 0;
unsigned long lastVolumeSend = 0;
int cryHitCount = 0;

struct SoundReading {
  int level;
  int minValue;
  int maxValue;
  int average;
  int samples;
};

String makeUrl(const char* path);
void connectWiFi();
SoundReading readSound();
void sendEvent(int level, bool crying);
void sendVolume(int level);
void logMeasurement(const SoundReading& reading, bool loudEnough, bool crying);
void logHttpResult(const char* label, int status, const String& response, const String& errorText);
String recordAudioWavBase64(int& level);
void writeWavHeader(uint8_t* wav, uint32_t dataSize);
void writeUInt16LE(uint8_t* target, uint16_t value);
void writeUInt32LE(uint8_t* target, uint32_t value);

void setup() {
  Serial.begin(115200);
  delay(200);
  Serial.println();
  Serial.println("Baby-Cry-Sensor startet");
  Serial.printf("Device: %s\n", DEVICE_ID);
  Serial.printf("Sound-Pin: %d, Cry-Threshold: %d\n", SOUND_PIN, CRY_THRESHOLD);
  Serial.printf("Server: http://%s:%u\n", SERVER_HOST, (unsigned int)SERVER_PORT);

  pinMode(SOUND_PIN, INPUT);
  analogReadResolution(12);
  analogSetPinAttenuation(SOUND_PIN, ADC_11db);
  connectWiFi();
}

void loop() {
  SoundReading reading = readSound();
  bool loudEnough = reading.level >= CRY_THRESHOLD;

  if (loudEnough) {
    if (cryHitCount < CRY_HITS_REQUIRED) {
      cryHitCount++;
    }
  } else {
    cryHitCount = 0;
  }

  bool crying = cryHitCount >= CRY_HITS_REQUIRED;
  bool shouldSendVolume = millis() - lastVolumeSend >= VOLUME_INTERVAL_MS;
  bool shouldSendEvent = crying || millis() - lastEventSend >= EVENT_INTERVAL_MS;

  if (shouldSendVolume) {
    logMeasurement(reading, loudEnough, crying);
    sendVolume(reading.level);
    lastVolumeSend = millis();
  }

  if (shouldSendEvent) {
    sendEvent(reading.level, crying);
    lastEventSend = millis();
  }

  delay(250);
}

SoundReading readSound() {
  int minValue = 4095;
  int maxValue = 0;
  uint32_t sum = 0;
  int samples = 0;
  unsigned long start = millis();

  while (millis() - start < SAMPLE_WINDOW_MS) {
    int value = analogRead(SOUND_PIN);
    if (value < minValue) minValue = value;
    if (value > maxValue) maxValue = value;
    sum += value;
    samples++;
    delayMicroseconds(250);
  }

  SoundReading reading;
  reading.level = maxValue - minValue;
  reading.minValue = minValue;
  reading.maxValue = maxValue;
  reading.average = samples > 0 ? sum / samples : 0;
  reading.samples = samples;
  return reading;
}

void sendEvent(int level, bool crying) {
  if (WiFi.status() != WL_CONNECTED) {
    connectWiFi();
  }

  String audioBase64 = "";
  int sensorLevel = level;
  int audioLevel = level;
  if (SEND_AUDIO_TO_AI) {
    Serial.printf("AUDIO Aufnahme fuer KI startet (%d Sekunden, %d Hz)\n", AUDIO_SECONDS, AUDIO_SAMPLE_RATE);
    audioBase64 = recordAudioWavBase64(audioLevel);
    if (audioBase64.length() > 0) {
      level = max(sensorLevel, audioLevel);
      crying = crying || level >= CRY_THRESHOLD;
      Serial.printf(
        "AUDIO bereit: sensorLevel=%d audioLevel=%d eventLevel=%d base64=%u Zeichen\n",
        sensorLevel,
        audioLevel,
        level,
        (unsigned int)audioBase64.length()
      );
    } else {
      Serial.println("AUDIO nicht verfuegbar, sende nur Sensorbewertung");
    }
  }

  HTTPClient http;
  String url = makeUrl("/api/events");
  Serial.printf("EVENT sende an %s\n", url.c_str());
  http.begin(url);
  http.setTimeout(30000);
  http.addHeader("Content-Type", "application/json");

  float confidence = crying ? 0.82 : 0.35;
  String body = "{";
  body += "\"deviceId\":\"";
  body += DEVICE_ID;
  body += "\",";
  body += "\"crying\":";
  body += crying ? "true" : "false";
  body += ",";
  body += "\"volume\":";
  body += String(level);
  body += ",";
  body += "\"confidence\":";
  body += String(confidence, 2);
  body += ",";
  body += "\"message\":\"";
  body += audioBase64.length() > 0 ? (crying ? "Sensor: Weinen moeglich, KI prueft Audio" : "KI-Audioanalyse angefragt") : (crying ? "Sensor: Weinen moeglich" : "Sensor: ruhig");
  body += "\"";
  if (audioBase64.length() > 0) {
    body += ",";
    body += "\"mimeType\":\"audio/wav\",";
    body += "\"audioBase64\":\"";
    body += audioBase64;
    body += "\"";
  }
  body += "}";

  int status = http.POST(body);
  String response = http.getString();
  String errorText = status < 0 ? http.errorToString(status) : "";
  if (audioBase64.length() > 0) {
    Serial.printf("EVENT Request: device=%s volume=%d audio=audio/wav base64=%u Zeichen\n", DEVICE_ID, level, (unsigned int)audioBase64.length());
  } else {
    Serial.printf("EVENT Request: %s\n", body.c_str());
  }
  logHttpResult("EVENT", status, response, errorText);
  http.end();
}

void sendVolume(int level) {
  if (WiFi.status() != WL_CONNECTED) {
    connectWiFi();
  }

  HTTPClient http;
  String url = makeUrl("/api/volume");
  Serial.printf("VOLUME sende an %s\n", url.c_str());
  http.begin(url);
  http.setTimeout(10000);
  http.addHeader("Content-Type", "application/json");

  String body = "{";
  body += "\"deviceId\":\"";
  body += DEVICE_ID;
  body += "\",";
  body += "\"volume\":";
  body += String(level);
  body += "}";

  int status = http.POST(body);
  String response = http.getString();
  String errorText = status < 0 ? http.errorToString(status) : "";
  Serial.printf("VOLUME Request: %s\n", body.c_str());
  logHttpResult("VOLUME", status, response, errorText);
  http.end();
}

String makeUrl(const char* path) {
  return String("http://") + SERVER_HOST + ":" + String(SERVER_PORT) + path;
}

String recordAudioWavBase64(int& level) {
  const uint32_t dataSize = AUDIO_SAMPLES;
  const uint32_t wavSize = WAV_HEADER_BYTES + dataSize;
  uint16_t* raw = (uint16_t*)malloc(AUDIO_SAMPLES * sizeof(uint16_t));
  if (raw == nullptr) {
    Serial.println("AUDIO Fehler: Kein Speicher fuer Rohdaten-Puffer");
    return "";
  }

  uint8_t* wav = (uint8_t*)malloc(wavSize);
  if (wav == nullptr) {
    Serial.println("AUDIO Fehler: Kein Speicher fuer WAV-Puffer");
    free(raw);
    return "";
  }

  writeWavHeader(wav, dataSize);

  int minValue = 4095;
  int maxValue = 0;
  uint32_t sum = 0;
  const uint32_t sampleIntervalUs = 1000000UL / AUDIO_SAMPLE_RATE;
  uint32_t nextSampleAt = micros();

  for (int i = 0; i < AUDIO_SAMPLES; i++) {
    int value = analogRead(SOUND_PIN);
    if (value < minValue) minValue = value;
    if (value > maxValue) maxValue = value;
    raw[i] = value;
    sum += value;

    nextSampleAt += sampleIntervalUs;
    while ((int32_t)(micros() - nextSampleAt) < 0) {
      delayMicroseconds(10);
    }
  }

  level = maxValue - minValue;
  int center = AUDIO_SAMPLES > 0 ? sum / AUDIO_SAMPLES : 2048;
  int maxDistance = max(maxValue - center, center - minValue);
  float gain = 1.0;
  if (maxDistance > 0) {
    gain = AUDIO_NORMALIZE_TARGET / maxDistance;
    if (gain > AUDIO_MAX_GAIN) gain = AUDIO_MAX_GAIN;
  }

  for (int i = 0; i < AUDIO_SAMPLES; i++) {
    int normalized = 128 + (int)((raw[i] - center) * gain);
    wav[WAV_HEADER_BYTES + i] = constrain(normalized, 0, 255);
  }
  free(raw);

  size_t encodedCapacity = ((wavSize + 2) / 3) * 4 + 1;
  unsigned char* encoded = (unsigned char*)malloc(encodedCapacity);
  if (encoded == nullptr) {
    Serial.println("AUDIO Fehler: Kein Speicher fuer Base64-Puffer");
    free(wav);
    return "";
  }

  size_t encodedLength = 0;
  int result = mbedtls_base64_encode(encoded, encodedCapacity, &encodedLength, wav, wavSize);
  free(wav);

  if (result != 0) {
    Serial.printf("AUDIO Fehler: Base64-Encoding fehlgeschlagen (%d)\n", result);
    free(encoded);
    return "";
  }

  encoded[encodedLength] = '\0';
  String audioBase64 = String((char*)encoded);
  free(encoded);
  return audioBase64;
}

void writeWavHeader(uint8_t* wav, uint32_t dataSize) {
  memcpy(wav, "RIFF", 4);
  writeUInt32LE(wav + 4, 36 + dataSize);
  memcpy(wav + 8, "WAVE", 4);
  memcpy(wav + 12, "fmt ", 4);
  writeUInt32LE(wav + 16, 16);
  writeUInt16LE(wav + 20, 1);
  writeUInt16LE(wav + 22, 1);
  writeUInt32LE(wav + 24, AUDIO_SAMPLE_RATE);
  writeUInt32LE(wav + 28, AUDIO_SAMPLE_RATE);
  writeUInt16LE(wav + 32, 1);
  writeUInt16LE(wav + 34, 8);
  memcpy(wav + 36, "data", 4);
  writeUInt32LE(wav + 40, dataSize);
}

void writeUInt16LE(uint8_t* target, uint16_t value) {
  target[0] = value & 0xff;
  target[1] = (value >> 8) & 0xff;
}

void writeUInt32LE(uint8_t* target, uint32_t value) {
  target[0] = value & 0xff;
  target[1] = (value >> 8) & 0xff;
  target[2] = (value >> 16) & 0xff;
  target[3] = (value >> 24) & 0xff;
}

void logMeasurement(const SoundReading& reading, bool loudEnough, bool crying) {
  Serial.printf(
    "MESSUNG level=%d min=%d max=%d avg=%d samples=%d threshold=%d hits=%d/%d status=%s wifi=%s\n",
    reading.level,
    reading.minValue,
    reading.maxValue,
    reading.average,
    reading.samples,
    CRY_THRESHOLD,
    cryHitCount,
    CRY_HITS_REQUIRED,
    crying ? "WEINEN_MOEGLICH" : "ruhig",
    WiFi.status() == WL_CONNECTED ? "verbunden" : "getrennt"
  );

  if (!loudEnough && reading.level < CRY_THRESHOLD / 2) {
    Serial.println("HINWEIS: Pegel ist sehr niedrig. AO statt DO nutzen, GND/VCC pruefen und Sensor-Poti empfindlicher drehen.");
  }
}

void logHttpResult(const char* label, int status, const String& response, const String& errorText) {
  bool ok = status >= 200 && status < 300;
  Serial.printf("%s Ergebnis: %s (HTTP %d)\n", label, ok ? "OK" : "FEHLER", status);

  if (status < 0 && errorText.length() > 0) {
    Serial.printf("%s Netzwerkfehler: %s\n", label, errorText.c_str());
  }

  if (response.length() > 0) {
    Serial.printf("%s Antwort: %s\n", label, response.c_str());
  } else {
    Serial.printf("%s Antwort: <leer>\n", label);
  }
}

void connectWiFi() {
  if (WiFi.status() == WL_CONNECTED) {
    Serial.printf("WLAN bereits verbunden: %s\n", WiFi.localIP().toString().c_str());
    return;
  }

  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  Serial.printf("Verbinde WLAN mit SSID \"%s\"", WIFI_SSID);
  unsigned long lastHint = millis();

  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");

    if (millis() - lastHint >= 30000) {
      Serial.println();
      Serial.println("WLAN noch nicht verbunden. SSID, Passwort und Signal pruefen.");
      Serial.print("Verbinde weiter");
      lastHint = millis();
    }
  }

  Serial.printf("\nVerbunden: %s\n", WiFi.localIP().toString().c_str());
}
