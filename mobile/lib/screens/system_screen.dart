import 'package:flutter/material.dart';

import '../services/settings_service.dart';

class SystemScreen extends StatefulWidget {
  const SystemScreen({super.key});

  @override
  State<SystemScreen> createState() => _SystemScreenState();
}

class _SystemScreenState extends State<SystemScreen> {
  final SettingsService _settingsService = SettingsService();

  List<Map<String, dynamic>> _systems = [];
  bool _isLoading = true;
  bool _isConnected = false;
  String _serverAddress = '';

  @override
  void initState() {
    super.initState();
    _settingsService.addListener(_onSettingsChanged);
    _loadData();
  }

  void _onSettingsChanged() {
    _loadData();
  }

  @override
  void dispose() {
    _settingsService.removeListener(_onSettingsChanged);
    super.dispose();
  }

  Future<void> _loadData() async {
    setState(() {
      _isLoading = true;
    });

    try {
      // Load server address
      _serverAddress = await _settingsService.getServerAddress();

      // Test connection
      _isConnected = await _settingsService.testConnection(_serverAddress);

      if (_isConnected) {
        // Load client data
        final systems = await _settingsService.getClientData();

        setState(() {
          _systems = systems;
        });
      } else {
        setState(() {
          _systems = [];
        });
      }
    } catch (e) {
      setState(() {
        _isConnected = false;
        _systems = [];
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Error loading client data: $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    } finally {
      setState(() {
        _isLoading = false;
      });
    }
  }

  Future<void> _toggleSystemStatus(String name, bool currentStatus) async {
    final newStatus = !currentStatus;

    final success = await _settingsService.updateClientStatus(name, newStatus);

    if (success) {
      // Update local state
      setState(() {
        final systemIndex = _systems.indexWhere(
          (system) => system['name'] == name,
        );
        if (systemIndex != -1) {
          _systems[systemIndex]['status'] = newStatus;
        }
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('$name ${newStatus ? 'enabled' : 'disabled'}'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(milliseconds: 500),
            content: Text(
              'Failed to update status. Check server connection.',
            ),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Widget _buildSystemsList() {
    if (!_isConnected) {
      return Card(
        child: SizedBox(
          width: double.infinity,
          child: Padding(
            padding: const EdgeInsets.all(24.0),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Icon(Icons.cloud_off, size: 64, color: Colors.grey.shade400),
                const SizedBox(height: 16),
                const Text(
                  'Server Not Connected',
                  style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                ),
                const SizedBox(height: 8),
                const Text(
                  'Please check your server settings and connection',
                  textAlign: TextAlign.center,
                  style: TextStyle(color: Colors.grey),
                ),
              ],
            ),
          ),
        ),
      );
    }

    if (_systems.isEmpty) {
      return Card(
        child: SizedBox(
          width: double.infinity,
          child: Padding(
            padding: const EdgeInsets.all(24.0),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Icon(Icons.settings, size: 64, color: Colors.blue.shade400),
                const SizedBox(height: 16),
                const Text(
                  'No Client Entries',
                  style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                ),
                const SizedBox(height: 8),
                const Text(
                  'No client entries available on the server',
                  textAlign: TextAlign.center,
                  style: TextStyle(color: Colors.grey),
                ),
              ],
            ),
          ),
        ),
      );
    }

    return ListView.builder(
      itemCount: _systems.length,
      itemBuilder: (context, index) {
        final system = _systems[index];
        final name = system['name'] as String;
        final status = system['status'] as bool;

        return GestureDetector(
          onTap: () => _toggleSystemStatus(name, status),
          child: Card(
            color: status ? Colors.green.shade600 : Colors.red.shade600,
            child: Padding(
              padding: const EdgeInsets.all(24.0),
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Icon(_getSystemIcon(name), size: 48, color: Colors.white),
                  const SizedBox(height: 8),
                  Text(
                    status ? 'Enabled' : 'Disabled',
                    style: const TextStyle(fontSize: 14, color: Colors.white),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }

  IconData _getSystemIcon(String systemName) {
    switch (systemName.toLowerCase()) {
      case 'power':
        return Icons.power_settings_new;
      case 'network':
        return Icons.network_check;
      case 'security':
        return Icons.security;
      case 'storage':
        return Icons.storage;
      default:
        return Icons.settings;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('System Control'),
        actions: [
          IconButton(onPressed: _loadData, icon: const Icon(Icons.refresh)),
        ],
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              onRefresh: _loadData,
              child: Padding(
                padding: const EdgeInsets.all(16.0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Connection Status Card
                    Card(
                      color: _isConnected
                          ? Colors.green.shade50
                          : Colors.red.shade50,
                      child: Padding(
                        padding: const EdgeInsets.all(16.0),
                        child: Row(
                          children: [
                            Icon(
                              _isConnected ? Icons.wifi : Icons.wifi_off,
                              color: _isConnected ? Colors.green : Colors.red,
                              size: 32,
                            ),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text(
                                    _isConnected
                                        ? 'Server Connected'
                                        : 'Server Disconnected',
                                    style: TextStyle(
                                      fontSize: 16,
                                      fontWeight: FontWeight.bold,
                                      color: _isConnected
                                          ? Colors.green.shade700
                                          : Colors.red.shade700,
                                    ),
                                  ),
                                  Text(
                                    _serverAddress,
                                    style: const TextStyle(
                                      fontSize: 14,
                                      color: Colors.grey,
                                    ),
                                  ),
                                ],
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),

                    const SizedBox(height: 24),

                    // System Controls Header
                    if (_isConnected) ...[
                      const Text(
                        'System Controls',
                        style: TextStyle(
                          fontSize: 18,
                          fontWeight: FontWeight.bold,
                        ),
                      ),
                      const SizedBox(height: 12),
                    ],

                    // Systems List - takes remaining space
                    Expanded(child: _buildSystemsList()),
                  ],
                ),
              ),
            ),
    );
  }
}
