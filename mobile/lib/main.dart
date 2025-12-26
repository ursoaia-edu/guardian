import 'dart:async';
import 'package:flutter/material.dart';
import 'screens/home_screen.dart';
import 'screens/settings_screen.dart';
import 'screens/system_screen.dart';
import 'services/settings_service.dart';

void main() {
  runApp(const ProcSentinelApp());
}

class ProcSentinelApp extends StatelessWidget {
  const ProcSentinelApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Guardian',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF2D3748),
          brightness: Brightness.light,
        ),
        useMaterial3: true,
        appBarTheme: const AppBarTheme(
          backgroundColor: Color(0xFF2D3748),
          foregroundColor: Colors.white,
          elevation: 2,
        ),
      ),
      home: const MainNavigation(),
      debugShowCheckedModeBanner: false,
    );
  }
}

class MainNavigation extends StatefulWidget {
  const MainNavigation({super.key});

  @override
  State<MainNavigation> createState() => _MainNavigationState();
}

class _MainNavigationState extends State<MainNavigation> with WidgetsBindingObserver {
  int _currentIndex = 0;
  final SettingsService _settingsService = SettingsService();
  bool _powerEnabled = true; // Default to true (green)
  Timer? _statusTimer;

  final List<Widget> _screens = [
    const HomeScreen(),
    const SystemScreen(),
    const SettingsScreen(),
  ];

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _checkPowerStatus();
    // Check power status every 10 seconds
    _statusTimer = Timer.periodic(const Duration(seconds: 10), (_) {
      _checkPowerStatus();
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _statusTimer?.cancel();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      // Refresh power status when app comes to foreground
      _checkPowerStatus();
    }
  }

  Future<void> _checkPowerStatus() async {
    try {
      final systems = await _settingsService.getSystemData();
      final powerSystem = systems.firstWhere(
        (system) => system['name'] == 'power',
        orElse: () => {'name': 'power', 'status': true},
      );

      if (mounted) {
        setState(() {
          _powerEnabled = powerSystem['status'] ?? true;
        });
      }
    } catch (e) {
      // If there's an error, default to enabled (green)
      if (mounted) {
        setState(() {
          _powerEnabled = true;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: _screens[_currentIndex],
      bottomNavigationBar: BottomNavigationBar(
        type: BottomNavigationBarType.fixed,
        currentIndex: _currentIndex,
        onTap: (index) {
          setState(() {
            _currentIndex = index;
          });
          // Refresh power status when System tab is tapped
          if (index == 1) {
            _checkPowerStatus();
          }
        },
        items: [
          const BottomNavigationBarItem(
            icon: Icon(Icons.security),
            label: 'Dashboard',
          ),
          BottomNavigationBarItem(
            icon: Icon(
              Icons.power_settings_new,
              color: _powerEnabled ? Colors.green : Colors.red,
            ),
            label: 'System',
          ),
          const BottomNavigationBarItem(
            icon: Icon(Icons.settings),
            label: 'Settings',
          ),
        ],
        selectedItemColor: const Color(0xFF2D3748),
        unselectedItemColor: Colors.grey,
      ),
    );
  }
}