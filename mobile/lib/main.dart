import 'package:flutter/material.dart';

import 'screens/computers_screen.dart';
import 'screens/home_screen.dart';
import 'screens/login_screen.dart';
import 'screens/settings_screen.dart';
import 'screens/system_screen.dart';
import 'services/settings_service.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await SettingsService().load();
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
      home: const _Root(),
      debugShowCheckedModeBanner: false,
    );
  }
}

/// Chooses between signing in and the app itself. The service is the single
/// source of truth for "is there a session", so signing in or out anywhere
/// swaps this without anyone navigating.
class _Root extends StatefulWidget {
  const _Root();

  @override
  State<_Root> createState() => _RootState();
}

class _RootState extends State<_Root> {
  final SettingsService _service = SettingsService();

  @override
  void initState() {
    super.initState();
    _service.addListener(_onChanged);
  }

  @override
  void dispose() {
    _service.removeListener(_onChanged);
    super.dispose();
  }

  void _onChanged() {
    if (mounted) setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    return _service.isSignedIn ? const MainNavigation() : const LoginScreen();
  }
}

class MainNavigation extends StatefulWidget {
  const MainNavigation({super.key});

  @override
  State<MainNavigation> createState() => _MainNavigationState();
}

class _MainNavigationState extends State<MainNavigation> {
  int _currentIndex = 0;

  final List<Widget> _screens = const [
    HomeScreen(),
    SystemScreen(),
    ComputersScreen(),
    SettingsScreen(),
  ];

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: IndexedStack(index: _currentIndex, children: _screens),
      bottomNavigationBar: BottomNavigationBar(
        type: BottomNavigationBarType.fixed,
        currentIndex: _currentIndex,
        onTap: (index) => setState(() => _currentIndex = index),
        items: const [
          BottomNavigationBarItem(icon: Icon(Icons.rule), label: 'Rules'),
          BottomNavigationBarItem(
            icon: Icon(Icons.meeting_room),
            label: 'Room',
          ),
          BottomNavigationBarItem(
            icon: Icon(Icons.computer),
            label: 'Computers',
          ),
          BottomNavigationBarItem(
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
