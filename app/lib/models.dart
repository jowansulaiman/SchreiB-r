class CryEvent {
  const CryEvent({
    required this.id,
    required this.deviceId,
    required this.alertType,
    required this.crying,
    required this.confidence,
    required this.volume,
    required this.source,
    required this.message,
    required this.createdAt,
  });

  final int id;
  final String deviceId;
  final String alertType;
  final bool crying;
  final double confidence;
  final double volume;
  final String source;
  final String message;
  final DateTime createdAt;

  bool get isCry => alertType == 'cry' || crying;
  bool get isLoud => alertType == 'loud';
  bool get isConnected => alertType == 'connected';
  bool get isAlert => isCry || isLoud;

  factory CryEvent.fromJson(Map<String, dynamic> json) {
    final crying = json['crying'] as bool? ?? false;
    return CryEvent(
      id: (json['id'] as num?)?.toInt() ?? 0,
      deviceId: json['deviceId'] as String? ?? 'unknown-device',
      alertType: json['alertType'] as String? ?? (crying ? 'cry' : 'quiet'),
      crying: crying,
      confidence: (json['confidence'] as num?)?.toDouble() ?? 0,
      volume: (json['volume'] as num?)?.toDouble() ?? 0,
      source: json['source'] as String? ?? 'server',
      message: json['message'] as String? ?? '',
      createdAt: DateTime.tryParse(json['createdAt'] as String? ?? '') ??
          DateTime.now(),
    );
  }
}

class VolumeReading {
  const VolumeReading({
    required this.id,
    required this.deviceId,
    required this.volume,
    required this.loud,
    required this.threshold,
    required this.message,
    required this.createdAt,
  });

  final int id;
  final String deviceId;
  final double volume;
  final bool loud;
  final double threshold;
  final String message;
  final DateTime createdAt;

  double get ratio {
    if (threshold <= 0) return 0;
    return (volume / threshold).clamp(0, 1.4);
  }

  factory VolumeReading.fromJson(Map<String, dynamic> json) {
    return VolumeReading(
      id: (json['id'] as num?)?.toInt() ?? 0,
      deviceId: json['deviceId'] as String? ?? 'unknown-device',
      volume: (json['volume'] as num?)?.toDouble() ?? 0,
      loud: json['loud'] as bool? ?? false,
      threshold: (json['threshold'] as num?)?.toDouble() ?? 0,
      message: json['message'] as String? ?? '',
      createdAt: DateTime.tryParse(json['createdAt'] as String? ?? '') ??
          DateTime.now(),
    );
  }
}
