#include <HTTPClient.h>
#include <WiFi.h>

const char* WIFI_SSID = "jowan";
const char* WIFI_PASSWORD = "199612";

// Muss die IP des Rechners sein, auf dem der Go-Server laeuft.
// Wenn du den Handy-Hotspot nutzt, aendert sich diese IP meistens.
const char* SERVER_HOST = "192.168.178.108";
const uint16_t SERVER_PORT = 8080;

const int SOUND_PIN = 34;
const int CRY_THRESHOLD = 900;
const unsigned long EVENT_INTERVAL_MS = 10000;
const unsigned long VOLUME_INTERVAL_MS = 3000;

unsigned long lastEventSend = 0;
unsigned long lastVolumeSend = 0;

String makeUrl(const char* path);
void connectWiFi();
int readPeakToPeak();
void sendEvent(int level, bool crying);
void sendVolume(int level);

void setup() {
  Serial.begin(115200);
  pinMode(SOUND_PIN, INPUT);
  connectWiFi();
}

void loop() {
  int level = readPeakToPeak();
  bool crying = level >= CRY_THRESHOLD;
  bool shouldSendVolume = millis() - lastVolumeSend >= VOLUME_INTERVAL_MS;
  bool shouldSendEvent = crying || millis() - lastEventSend >= EVENT_INTERVAL_MS;

  if (shouldSendVolume) {
    sendVolume(level);
    lastVolumeSend = millis();
  }

  if (shouldSendEvent) {
    sendEvent(level, crying);
    lastEventSend = millis();
  }

  delay(250);
}

int readPeakToPeak() {
  int minValue = 4095;
  int maxValue = 0;
  unsigned long start = millis();

  while (millis() - start < 60) {
    int value = analogRead(SOUND_PIN);
    if (value < minValue) minValue = value;
    if (value > maxValue) maxValue = value;
  }

  return maxValue - minValue;
}

void sendEvent(int level, bool crying) {
  if (WiFi.status() != WL_CONNECTED) {
    connectWiFi();
  }

  HTTPClient http;
  http.begin(makeUrl("/api/events"));
  http.addHeader("Content-Type", "application/json");

  float confidence = crying ? 0.82 : 0.35;
  String body = "{";
  body += "\"deviceId\":\"esp32-1\",";
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
  body += crying ? "Sensor: Weinen moeglich" : "Sensor: ruhig";
  body += "\"";
  body += "}";

  int status = http.POST(body);
  Serial.printf("POST %d: %s\n", status, body.c_str());
  http.end();
}

void sendVolume(int level) {
  if (WiFi.status() != WL_CONNECTED) {
    connectWiFi();
  }

  HTTPClient http;
  http.begin(makeUrl("/api/volume"));
  http.addHeader("Content-Type", "application/json");

  String body = "{";
  body += "\"deviceId\":\"esp32-1\",";
  body += "\"volume\":";
  body += String(level);
  body += "}";

  int status = http.POST(body);
  Serial.printf("VOLUME %d: %s\n", status, body.c_str());
  http.end();
}

String makeUrl(const char* path) {
  return String("http://") + SERVER_HOST + ":" + String(SERVER_PORT) + path;
}

void connectWiFi() {
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  Serial.print("Verbinde WLAN");

  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
  }

  Serial.printf("\nVerbunden: %s\n", WiFi.localIP().toString().c_str());
}
