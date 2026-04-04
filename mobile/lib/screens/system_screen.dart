import 'package:flutter/material.dart';

import '../services/settings_service.dart';
import '../utils/snackbar_helper.dart';

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

  @override
  void initState() {
    super.initState();
    _settingsService.addListener(_onSettingsChanged);
    _settingsService.serverEnabledNotifier.addListener(_onServerEnabledChanged);
    _loadData();
  }

  void _onSettingsChanged() {
    _loadData();
  }

  void _onServerEnabledChanged() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _settingsService.removeListener(_onSettingsChanged);
    _settingsService.serverEnabledNotifier.removeListener(_onServerEnabledChanged);
    super.dispose();
  }

  Future<void> _loadData() async {
    setState(() {
      _isLoading = true;
    });

    try {
      final serverAddress = await _settingsService.getServerAddress();
      _isConnected = await _settingsService.testConnection(serverAddress);

      if (_isConnected) {
        final results = await Future.wait([
          _settingsService.getClientData(),
          _settingsService.getServerStatus(),
        ]);
        setState(() {
          _systems = results[0] as List<Map<String, dynamic>>;
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
        showSnackBarMessage(
          context,
          message: 'Error loading data: $e',
          backgroundColor: Colors.red,

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

    // Confirmation for dangerous actions (e.g. disabling power)
    if (name == 'power' && !newStatus) {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Disable Power?'),
          content: const Text(
            'This will trigger a shutdown on connected computers.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Cancel'),
            ),
            TextButton(
              onPressed: () => Navigator.of(context).pop(true),
              style: TextButton.styleFrom(foregroundColor: Colors.red),
              child: const Text('Disable'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
    }

    final success = await _settingsService.updateClientStatus(name, newStatus);

    if (success) {
      setState(() {
        final systemIndex = _systems.indexWhere(
          (system) => system['name'] == name,
        );
        if (systemIndex != -1) {
          _systems[systemIndex]['status'] = newStatus;
        }
      });

      if (mounted) {
        showSnackBarMessage(
          context,
          message: '$name ${newStatus ? 'enabled' : 'disabled'}',
          backgroundColor: Colors.green,
        );
      }
    } else {
      if (mounted) {
        showSnackBarMessage(
          context,
          message: 'Failed to update status',
          backgroundColor: Colors.red,

        );
      }
    }
  }

  Widget _buildDisconnected() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.cloud_off, size: 72, color: Colors.grey.shade400),
          const SizedBox(height: 16),
          const Text(
            'Server Not Connected',
            style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          const Text(
            'Check your server settings and connection',
            textAlign: TextAlign.center,
            style: TextStyle(color: Colors.grey),
          ),
          const SizedBox(height: 24),
          OutlinedButton.icon(
            onPressed: _loadData,
            icon: const Icon(Icons.refresh),
            label: const Text('Retry'),
          ),
        ],
      ),
    );
  }

  Widget _buildEmpty() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.settings, size: 72, color: Colors.grey.shade300),
          const SizedBox(height: 16),
          const Text(
            'No Client Entries',
            style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          const Text(
            'No entries available on the server',
            textAlign: TextAlign.center,
            style: TextStyle(color: Colors.grey),
          ),
        ],
      ),
    );
  }

  Widget _buildSystemGrid() {
    return GridView.builder(
      padding: const EdgeInsets.all(16),
      gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
        crossAxisCount: 2,
        crossAxisSpacing: 12,
        mainAxisSpacing: 12,
        childAspectRatio: 1.2,
      ),
      itemCount: _systems.length,
      itemBuilder: (context, index) {
        final system = _systems[index];
        final name = system['name'] as String;
        final status = system['status'] as bool;

        return GestureDetector(
          onTap: () => _toggleSystemStatus(name, status),
          child: Card(
            elevation: 2,
            color: status ? Colors.green.shade600 : Colors.red.shade600,
            child: Padding(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Icon(_getSystemIcon(name), size: 40, color: Colors.white),
                  const SizedBox(height: 10),
                  Text(
                    name[0].toUpperCase() + name.substring(1),
                    style: const TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                      color: Colors.white,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    status ? 'Enabled' : 'Disabled',
                    style: TextStyle(
                      fontSize: 12,
                      color: Colors.white.withValues(alpha: 0.8),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }

  Future<void> _toggleServerStatus() async {
    final newStatus = !_settingsService.serverEnabled;
    final success = await _settingsService.toggleServerStatus(newStatus);

    if (!success && mounted) {
      showSnackBarMessage(
        context,
        message: 'Failed to update server status',
        backgroundColor: Colors.red,
      );
    }
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
          if (_isConnected)
            IconButton(
              onPressed: _toggleServerStatus,
              icon: Icon(
                _settingsService.serverEnabled ? Icons.shield : Icons.shield_outlined,
                color: _settingsService.serverEnabled ? Colors.green : Colors.orange,
              ),
              tooltip: _settingsService.serverEnabled ? 'Disable server' : 'Enable server',
            ),
        ],
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : !_isConnected
              ? _buildDisconnected()
              : RefreshIndicator(
                  onRefresh: _loadData,
                  child: _systems.isEmpty
                      ? ListView(children: [
                          SizedBox(
                            height: MediaQuery.of(context).size.height * 0.6,
                            child: _buildEmpty(),
                          ),
                        ])
                      : _buildSystemGrid(),
                ),
    );
  }
}
