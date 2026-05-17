import 'package:flutter/material.dart';

import 'models.dart';

class DashboardStatus {
  const DashboardStatus({
    required this.title,
    required this.message,
    required this.color,
    required this.icon,
  });

  final String title;
  final String message;
  final Color color;
  final IconData icon;
}

class StatusCard extends StatelessWidget {
  const StatusCard({super.key, required this.status, required this.event});

  final DashboardStatus status;
  final CryEvent? event;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: status.color.withValues(alpha: 0.10),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: status.color.withValues(alpha: 0.35)),
      ),
      child: Row(
        children: [
          Container(
            width: 56,
            height: 56,
            decoration:
                BoxDecoration(color: status.color, shape: BoxShape.circle),
            child: Icon(status.icon, color: Colors.white, size: 28),
          ),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  status.title,
                  style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                        fontWeight: FontWeight.w800,
                      ),
                ),
                const SizedBox(height: 4),
                Text(
                  status.message,
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
                if (event != null) ...[
                  const SizedBox(height: 10),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      InfoChip(icon: Icons.memory, label: event!.deviceId),
                      InfoChip(icon: Icons.hub, label: event!.source),
                      InfoChip(
                        icon: Icons.speed,
                        label: event!.volume.toStringAsFixed(0),
                      ),
                    ],
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class VolumeCard extends StatelessWidget {
  const VolumeCard({super.key, required this.reading});

  final VolumeReading? reading;

  @override
  Widget build(BuildContext context) {
    final loud = reading?.loud ?? false;
    final color = loud ? const Color(0xFFF97316) : const Color(0xFF2563EB);
    final ratio = reading?.ratio ?? 0.0;

    return Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          PanelTitle(
            icon: Icons.graphic_eq,
            title: 'Lautstaerke',
            color: color,
          ),
          const SizedBox(height: 16),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Text(
                reading == null ? '--' : reading!.volume.toStringAsFixed(0),
                style: Theme.of(context).textTheme.displaySmall?.copyWith(
                      fontWeight: FontWeight.w800,
                      letterSpacing: 0,
                    ),
              ),
              const SizedBox(width: 8),
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Text(
                  reading == null
                      ? ''
                      : '/ ${reading!.threshold.toStringAsFixed(0)}',
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          ClipRRect(
            borderRadius: BorderRadius.circular(6),
            child: LinearProgressIndicator(
              minHeight: 10,
              value: ratio.clamp(0, 1),
              backgroundColor: const Color(0xFFE5E7EB),
              color: color,
            ),
          ),
          const SizedBox(height: 10),
          Text(reading?.message ?? 'Noch kein Messwert'),
        ],
      ),
    );
  }
}

class ServerCard extends StatelessWidget {
  const ServerCard({
    super.key,
    required this.controller,
    required this.error,
    required this.onSubmitted,
  });

  final TextEditingController controller;
  final String? error;
  final VoidCallback onSubmitted;

  @override
  Widget build(BuildContext context) {
    return Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const PanelTitle(
            icon: Icons.router,
            title: 'Server',
            color: Color(0xFF2563EB),
          ),
          const SizedBox(height: 14),
          TextField(
            controller: controller,
            decoration: const InputDecoration(
              border: OutlineInputBorder(),
              prefixIcon: Icon(Icons.link),
              labelText: 'URL',
            ),
            keyboardType: TextInputType.url,
            onSubmitted: (_) => onSubmitted(),
          ),
          if (error != null) ...[
            const SizedBox(height: 10),
            Text(
              error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ],
        ],
      ),
    );
  }
}

class SoundSettingsCard extends StatelessWidget {
  const SoundSettingsCard({
    super.key,
    required this.onSound,
    required this.soundEnabled,
    required this.error,
  });

  final VoidCallback onSound;
  final bool soundEnabled;
  final String? error;

  @override
  Widget build(BuildContext context) {
    return Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const PanelTitle(
            icon: Icons.volume_up,
            title: 'Ton',
            color: Color(0xFF2563EB),
          ),
          const SizedBox(height: 14),
          SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              onPressed: onSound,
              icon: Icon(soundEnabled ? Icons.volume_off : Icons.volume_up),
              label: Text(soundEnabled ? 'Ton aus' : 'Ton ein'),
            ),
          ),
          if (error != null) ...[
            const SizedBox(height: 10),
            Text(
              error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ],
        ],
      ),
    );
  }
}

class EventList extends StatelessWidget {
  const EventList({super.key, required this.events});

  final List<CryEvent> events;

  @override
  Widget build(BuildContext context) {
    return Panel(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const PanelTitle(
            icon: Icons.history,
            title: 'Verlauf',
            color: Color(0xFF334155),
          ),
          const SizedBox(height: 8),
          Expanded(
            child: events.isEmpty
                ? const Center(child: Text('Keine Events'))
                : ListView.separated(
                    itemCount: events.length,
                    separatorBuilder: (_, __) => const Divider(height: 1),
                    itemBuilder: (context, index) {
                      final event = events[index];
                      final color = event.isCry
                          ? const Color(0xFFDC2626)
                          : event.isLoud
                              ? const Color(0xFFF97316)
                              : const Color(0xFF16A34A);
                      final icon = event.isCry
                          ? Icons.notifications_active
                          : event.isLoud
                              ? Icons.volume_up
                              : Icons.check_circle;

                      return ListTile(
                        contentPadding: EdgeInsets.zero,
                        leading: Icon(icon, color: color),
                        title: Text(
                          event.message,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                        subtitle: Text(
                          '${event.deviceId} - ${event.source}',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                        trailing: Column(
                          mainAxisAlignment: MainAxisAlignment.center,
                          crossAxisAlignment: CrossAxisAlignment.end,
                          children: [
                            Text(
                              _time(event.createdAt),
                              style:
                                  const TextStyle(fontWeight: FontWeight.w700),
                            ),
                            Text('${(event.confidence * 100).round()}%'),
                          ],
                        ),
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }

  String _time(DateTime value) {
    final local = value.toLocal();
    final hour = local.hour.toString().padLeft(2, '0');
    final minute = local.minute.toString().padLeft(2, '0');
    return '$hour:$minute';
  }
}

class Panel extends StatelessWidget {
  const Panel({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: const Color(0xFFE5E7EB)),
      ),
      child: child,
    );
  }
}

class PanelTitle extends StatelessWidget {
  const PanelTitle({
    super.key,
    required this.icon,
    required this.title,
    required this.color,
  });

  final IconData icon;
  final String title;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(icon, color: color),
        const SizedBox(width: 8),
        Text(
          title,
          style: Theme.of(context).textTheme.titleMedium?.copyWith(
                fontWeight: FontWeight.w800,
              ),
        ),
      ],
    );
  }
}

class InfoChip extends StatelessWidget {
  const InfoChip({super.key, required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: Colors.white.withValues(alpha: 0.75),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: const Color(0xFFE5E7EB)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 16),
          const SizedBox(width: 6),
          Flexible(
            child: Text(
              label,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}
