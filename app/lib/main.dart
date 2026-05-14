import 'package:flutter/material.dart';

import 'dashboard_page.dart';

void main() => runApp(const MeidanApp());

class MeidanApp extends StatelessWidget {
  const MeidanApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Meidan',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xFF2563EB)),
        scaffoldBackgroundColor: const Color(0xFFF6F8FB),
        useMaterial3: true,
      ),
      home: const DashboardPage(),
    );
  }
}
