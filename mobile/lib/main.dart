import 'dart:async';
import 'package:flutter/material.dart';
import 'screens/home_screen.dart';
import 'screens/settings_screen.dart';
import 'screens/system_screen.dart';
import 'screens/computers_screen.dart';
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
  bool _powerEnabled = true;
  bool _isConnected = false;
  Timer? _statusTimer;

  final List<Widget> _screens = [
    const HomeScreen(),
    const SystemScreen(),
    const ComputersScreen(),
    const SettingsScreen(),
  ];

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _settingsService.addListener(_checkStatus);
    _checkStatus();
    _statusTimer = Timer.periodic(const Duration(seconds: 10), (_) {
      _checkStatus();
    });
  }

  @override
  void dispose() {
    _settingsService.removeListener(_checkStatus);
    WidgetsBinding.instance.removeObserver(this);
    _statusTimer?.cancel();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _checkStatus();
    }
  }

  Future<void> _checkStatus() async {
    try {
      final serverAddress = await _settingsService.getServerAddress();
      final connected = await _settingsService.testConnection(serverAddress);

      bool power = true;
      if (connected) {
        final systems = await _settingsService.getClientData();
        final powerSystem = systems.firstWhere(
          (system) => system['name'] == 'power',
          orElse: () => {'name': 'power', 'status': true},
        );
        power = powerSystem['status'] ?? true;
      }

      if (mounted) {
        setState(() {
          _isConnected = connected;
          _powerEnabled = power;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _isConnected = false;
          _powerEnabled = true;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: IndexedStack(
        index: _currentIndex,
        children: _screens,
      ),
      bottomNavigationBar: BottomNavigationBar(
        type: BottomNavigationBarType.fixed,
        currentIndex: _currentIndex,
        onTap: (index) {
          setState(() {
            _currentIndex = index;
          });
          if (index == 1) {
            _checkStatus();
          }
        },
        items: [
          BottomNavigationBarItem(
            icon: Icon(
              _isConnected ? Icons.security : Icons.cloud_off,
              color: _currentIndex == 0
                  ? null
                  : (_isConnected ? null : Colors.red.shade300),
            ),
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
            icon: Icon(Icons.computer),
            label: 'Computers',
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
