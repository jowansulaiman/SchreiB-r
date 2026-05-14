import 'dart:convert';

import 'package:audioplayers/audioplayers.dart';
import 'package:http/http.dart' as http;

import 'models.dart';

class DashboardData {
  const DashboardData({
    required this.events,
    required this.volumes,
  });

  final List<CryEvent> events;
  final List<VolumeReading> volumes;
}

class MeidanApi {
  const MeidanApi(this.serverUrl);

  final String serverUrl;

  String get _baseUrl => serverUrl.trim().replaceAll(RegExp(r'/+$'), '');

  Future<DashboardData> fetchDashboardData() async {
    final responses = await Future.wait([
      http
          .get(Uri.parse('$_baseUrl/api/events'))
          .timeout(const Duration(seconds: 4)),
      http
          .get(Uri.parse('$_baseUrl/api/volume'))
          .timeout(const Duration(seconds: 4)),
    ]);

    if (responses[0].statusCode != 200) {
      throw Exception('Events: ${responses[0].statusCode}');
    }
    if (responses[1].statusCode != 200) {
      throw Exception('Lautstaerke: ${responses[1].statusCode}');
    }

    final eventJson = jsonDecode(responses[0].body) as List<dynamic>;
    final volumeJson = jsonDecode(responses[1].body) as List<dynamic>;
    return DashboardData(
      events: eventJson
          .cast<Map<String, dynamic>>()
          .map(CryEvent.fromJson)
          .toList(),
      volumes: volumeJson
          .cast<Map<String, dynamic>>()
          .map(VolumeReading.fromJson)
          .toList(),
    );
  }

  Future<void> sendCryTest(bool crying) {
    return post('/api/events', {
      'deviceId': 'flutter-test',
      'crying': crying,
      'confidence': crying ? 0.92 : 0.78,
      'volume': crying ? 86 : 28,
      'message': crying ? 'Test: Weinen erkannt' : 'Test: ruhig',
    });
  }

  Future<void> sendVolumeTest(bool loud) {
    return post('/api/volume', {
      'deviceId': 'flutter-volume-test',
      'volume': loud ? 1400 : 260,
      'message': loud ? 'Testpegel sehr laut' : 'Testpegel normal',
    });
  }

  Future<void> post(String path, Map<String, Object> payload) async {
    final response = await http
        .post(
          Uri.parse('$_baseUrl$path'),
          headers: {'Content-Type': 'application/json'},
          body: jsonEncode(payload),
        )
        .timeout(const Duration(seconds: 4));
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw Exception('Server antwortet mit ${response.statusCode}');
    }
  }
}

class AlertAudioService {
  AlertAudioService() {
    _player.audioCache = AudioCache(prefix: '');
  }

  static const _cryAsset = 'Assets/warn.mp3';
  static const _loudAsset = 'Assets/warn.mp3';

  final AudioPlayer _player = AudioPlayer();

  Future<void> prepare() async {
    await _player.audioCache.loadAll([_cryAsset]);
    await _player.setVolume(1);
  }

  Future<void> stop() => _player.stop();

  Future<void> play(CryEvent event) async {
    final asset = event.isCry ? _cryAsset : _loudAsset;
    await _player.stop();
    await _player.play(AssetSource(asset));
  }

  void dispose() {
    _player.dispose();
  }
}
