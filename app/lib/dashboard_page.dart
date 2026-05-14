import 'dart:async';

import 'package:flutter/material.dart';

import 'dashboard_widgets.dart';
import 'models.dart';
import 'services.dart';

class DashboardPage extends StatefulWidget {
  const DashboardPage({super.key});

  @override
  State<DashboardPage> createState() => _DashboardPageState();
}

class _DashboardPageState extends State<DashboardPage> {
  static const _defaultServer = String.fromEnvironment(
    'SERVER_URL',
    defaultValue: 'http://localhost:8080',
  );

  final _serverController = TextEditingController(text: _defaultServer);
  final _audio = AlertAudioService();
  final List<CryEvent> _events = [];
  final List<VolumeReading> _volumes = [];
  Timer? _timer;
  bool _loading = false;
  bool _soundEnabled = false;
  bool _receivedInitialData = false;
  String? _error;
  String? _soundError;
  int? _lastAlertId;

  CryEvent? get _latestEvent => _events.isEmpty ? null : _events.first;
  VolumeReading? get _latestVolume => _volumes.isEmpty ? null : _volumes.first;
  String get _serverUrl =>
      _serverController.text.trim().replaceAll(RegExp(r'/+$'), '');

  @override
  void initState() {
    super.initState();
    _loadData();
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => _loadData());
  }

  @override
  void dispose() {
    _timer?.cancel();
    _audio.dispose();
    _serverController.dispose();
    super.dispose();
  }

  Future<void> _loadData() async {
    if (_loading || _serverUrl.isEmpty) return;
    setState(() {
      _loading = true;
      _error = null;
    });

    try {
      final data = await MeidanApi(_serverUrl).fetchDashboardData();
      final alert = _latestAlert(data.events);

      if (!mounted) return;
      setState(() {
        _events
          ..clear()
          ..addAll(data.events);
        _volumes
          ..clear()
          ..addAll(data.volumes);
      });

      final shouldPlay =
          _soundEnabled && _receivedInitialData && alert?.id != _lastAlertId;
      _receivedInitialData = true;
      if (alert != null) {
        _lastAlertId = alert.id;
      }
      if (alert != null && shouldPlay) {
        await _playAlertSound(alert);
      }
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _sendCryTest(bool crying) async {
    await _send(() => MeidanApi(_serverUrl).sendCryTest(crying));
  }

  Future<void> _sendVolumeTest(bool loud) async {
    await _send(() => MeidanApi(_serverUrl).sendVolumeTest(loud));
  }

  Future<void> _send(Future<void> Function() action) async {
    try {
      await action();
      await _loadData();
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  Future<void> _toggleSound() async {
    if (_soundEnabled) {
      await _audio.stop();
      if (mounted) {
        setState(() {
          _soundEnabled = false;
          _soundError = null;
        });
      }
      return;
    }

    try {
      await _audio.prepare();
      if (mounted) {
        setState(() {
          _soundEnabled = true;
          _soundError = null;
        });
      }
    } catch (error) {
      if (mounted) {
        setState(() {
          _soundEnabled = false;
          _soundError = 'Ton konnte nicht vorbereitet werden: $error';
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final status = _status();
    final visibleError = _error ?? _soundError;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Meidan'),
        backgroundColor: Colors.white,
        surfaceTintColor: Colors.white,
        actions: [
          IconButton(
            onPressed: _loading ? null : _loadData,
            tooltip: 'Aktualisieren',
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              StatusCard(status: status, event: _latestEvent),
              const SizedBox(height: 12),
              _responsiveRow(
                children: [
                  VolumeCard(reading: _latestVolume),
                  ServerCard(
                    controller: _serverController,
                    error: visibleError,
                    onSubmitted: _loadData,
                  ),
                ],
              ),
              const SizedBox(height: 12),
              DashboardActionBar(
                onCry: () => _sendCryTest(true),
                onLoud: () => _sendVolumeTest(true),
                onQuiet: () => _sendCryTest(false),
                onSound: _toggleSound,
                soundEnabled: _soundEnabled,
              ),
              const SizedBox(height: 16),
              Expanded(child: EventList(events: _events)),
            ],
          ),
        ),
      ),
    );
  }

  DashboardStatus _status() {
    final event = _latestEvent;
    if (event == null) {
      return const DashboardStatus(
        title: 'Bereit',
        message: 'Noch keine Meldungen',
        color: Color(0xFF2563EB),
        icon: Icons.sensors,
      );
    }
    if (event.isCry) {
      return DashboardStatus(
        title: 'Weinen erkannt',
        message: event.message,
        color: const Color(0xFFDC2626),
        icon: Icons.notifications_active,
      );
    }
    if (event.isLoud) {
      return DashboardStatus(
        title: 'Sehr laut',
        message: event.message,
        color: const Color(0xFFF97316),
        icon: Icons.volume_up,
      );
    }
    return DashboardStatus(
      title: 'Alles ruhig',
      message: event.message,
      color: const Color(0xFF16A34A),
      icon: Icons.check,
    );
  }

  Widget _responsiveRow({required List<Widget> children}) {
    return LayoutBuilder(
      builder: (context, constraints) {
        if (constraints.maxWidth < 720) {
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              children[0],
              const SizedBox(height: 12),
              children[1],
            ],
          );
        }
        return Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(child: children[0]),
            const SizedBox(width: 12),
            Expanded(child: children[1]),
          ],
        );
      },
    );
  }

  CryEvent? _latestAlert(List<CryEvent> events) {
    for (final event in events) {
      if (event.isAlert) return event;
    }
    return null;
  }

  Future<void> _playAlertSound(CryEvent event) async {
    try {
      await _audio.play(event);
      if (_soundError != null && mounted) {
        setState(() => _soundError = null);
      }
    } catch (error) {
      if (mounted) {
        setState(() {
          _soundError = 'Ton konnte nicht abgespielt werden: $error';
        });
      }
    }
  }
}
