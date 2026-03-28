import 'package:flutter/material.dart';

import '../services/settings_service.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  final SettingsService _settingsService = SettingsService();
  final TextEditingController _addAppController = TextEditingController();

  List<Map<String, dynamic>> _blockedApps = [];
  bool _serverEnabled = false;
  String _serverMode = 'blacklist';
  String _serverAddress = '';
  bool _isLoading = true;
  bool _isConnected = false;

  @override
  void initState() {
    super.initState();
    _settingsService.addListener(_onSettingsChanged);
    _loadData();
  }

  void _onSettingsChanged() {
    _loadData();
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
        // Load server data
        final apps = await _settingsService.getBlockedApplications();
        final status = await _settingsService.getServerStatus();

        setState(() {
          _blockedApps = apps;
          _serverEnabled = status['enabled'] as bool;
          _serverMode = status['mode'] as String;
        });
      } else {
        setState(() {
          _blockedApps = [];
          _serverEnabled = false;
          _serverMode = 'blacklist';
        });
      }
    } catch (e) {
      setState(() {
        _isConnected = false;
        _blockedApps = [];
        _serverEnabled = false;
        _serverMode = 'blacklist';
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('Error loading data: $e'),
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

  Future<void> _addApplication() async {
    final appName = _addAppController.text.trim();

    if (appName.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          duration: Duration(milliseconds: 500),
          content: Text('Please enter an application name'),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    if (_blockedApps.any((app) => app['name'] == appName)) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          duration: Duration(milliseconds: 500),
          content: Text('Application is already in the list'),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    final success = await _settingsService.addBlockedApplication(appName, mode: _serverMode);

    if (success) {
      // Fetch the newly added application with its ID
      final apps = await _settingsService.getBlockedApplications();
      setState(() {
        _blockedApps = apps;
      });
      _addAppController.clear();

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('Added "$appName" ($_serverMode)'),
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
              'Failed to add application. Check server connection.',
            ),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _removeApplication(String appName) async {
    final success = await _settingsService.removeBlockedApplication(appName);

    if (success) {
      setState(() {
        _blockedApps.removeWhere((app) => app['name'] == appName);
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('Removed "$appName"'),
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
              'Failed to remove application. Check server connection.',
            ),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _updateApplicationStatus(String name, bool enabled) async {
    final success = await _settingsService.updateApplicationStatus(
      name,
      enabled,
    );

    if (success) {
      setState(() {
        final index = _blockedApps.indexWhere((app) => app['name'] == name);
        if (index != -1) {
          _blockedApps[index]['enabled'] = enabled;
        }
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('$name ${enabled ? 'enabled' : 'disabled'}'),
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
              'Failed to update application. Check server connection.',
            ),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _resetApplications() async {
    // Show confirmation dialog
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Reset Applications'),
        content: const Text(
          'Are you sure you want to remove all applications?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            style: TextButton.styleFrom(foregroundColor: Colors.red),
            child: const Text('Reset'),
          ),
        ],
      ),
    );

    if (confirmed == true) {
      final success = await _settingsService.resetBlockedApplications();

      if (success) {
        setState(() {
          _blockedApps.clear();
        });

        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              duration: Duration(milliseconds: 500),
              content: Text('All applications removed'),
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
                'Failed to reset applications. Check server connection.',
              ),
              backgroundColor: Colors.red,
            ),
          );
        }
      }
    }
  }

  Future<void> _toggleServerStatus() async {
    final newStatus = !_serverEnabled;
    final success = await _settingsService.toggleServerStatus(newStatus);

    if (success) {
      setState(() {
        _serverEnabled = newStatus;
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('Server ${newStatus ? 'enabled' : 'disabled'}'),
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
              'Failed to update server status. Check server connection.',
            ),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _setMode(String mode) async {
    final success = await _settingsService.toggleServerStatus(_serverEnabled, mode: mode);

    if (success) {
      setState(() {
        _serverMode = mode;
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('Mode changed to $mode'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(milliseconds: 500),
            content: Text('Failed to change mode. Check server connection.'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Color _modeColor(String mode) {
    return mode == 'whitelist' ? Colors.blue : Colors.deepOrange;
  }

  Widget _buildApplicationsList() {
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

    if (_blockedApps.isEmpty) {
      return Card(
        child: SizedBox(
          width: double.infinity,
          child: Padding(
            padding: const EdgeInsets.all(24.0),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Icon(Icons.security, size: 64, color: Colors.green.shade400),
                const SizedBox(height: 16),
                const Text(
                  'No Applications',
                  style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                ),
                const SizedBox(height: 8),
                const Text(
                  'Add applications to monitor and control',
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
      itemCount: _blockedApps.length,
      itemBuilder: (context, index) {
        final app = _blockedApps[index];
        final appName = app['name'] as String;
        final enabled = app['enabled'] as bool;
        final mode = (app['mode'] as String?) ?? 'blacklist';
        return Card(
          child: ListTile(
            leading: IconButton(
              onPressed: () => _removeApplication(appName),
              icon: const Icon(Icons.delete, color: Colors.red),
              tooltip: 'Remove application',
            ),
            title: Text(appName),
            subtitle: Text(
              mode,
              style: TextStyle(
                fontSize: 12,
                color: _modeColor(mode),
                fontWeight: FontWeight.w500,
              ),
            ),
            trailing: Switch(
              value: enabled,
              onChanged: (newValue) =>
                  _updateApplicationStatus(appName, newValue),
              inactiveThumbColor: Colors.grey,
              inactiveTrackColor: Colors.grey.shade300,
            ),
          ),
        );
      },
    );
  }

  @override
  void dispose() {
    _settingsService.removeListener(_onSettingsChanged);
    _addAppController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Guardian Dashboard'),
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
                    // Server Status Card
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
                                  if (_isConnected)
                                    Text(
                                      'Status: ${_serverEnabled ? 'Enabled' : 'Disabled'}',
                                      style: TextStyle(
                                        fontSize: 14,
                                        color: _serverEnabled
                                            ? Colors.green.shade700
                                            : Colors.orange.shade700,
                                      ),
                                    ),
                                ],
                              ),
                            ),
                            if (_isConnected)
                              Switch(
                                value: _serverEnabled,
                                onChanged: (_) => _toggleServerStatus(),
                              ),
                          ],
                        ),
                      ),
                    ),

                    const SizedBox(height: 16),

                    // Mode Toggle
                    if (_isConnected)
                      SizedBox(
                        width: double.infinity,
                        child: SegmentedButton<String>(
                          segments: [
                            ButtonSegment<String>(
                              value: 'blacklist',
                              label: const Text('Blacklist'),
                              icon: Icon(
                                Icons.block,
                                color: _serverMode == 'blacklist'
                                    ? Colors.white
                                    : Colors.deepOrange,
                              ),
                            ),
                            ButtonSegment<String>(
                              value: 'whitelist',
                              label: const Text('Whitelist'),
                              icon: Icon(
                                Icons.check_circle_outline,
                                color: _serverMode == 'whitelist'
                                    ? Colors.white
                                    : Colors.blue,
                              ),
                            ),
                          ],
                          selected: {_serverMode},
                          onSelectionChanged: (selected) {
                            _setMode(selected.first);
                          },
                          style: ButtonStyle(
                            backgroundColor: WidgetStateProperty.resolveWith(
                              (states) {
                                if (states.contains(WidgetState.selected)) {
                                  return _serverMode == 'blacklist'
                                      ? Colors.deepOrange
                                      : Colors.blue;
                                }
                                return null;
                              },
                            ),
                            foregroundColor: WidgetStateProperty.resolveWith(
                              (states) {
                                if (states.contains(WidgetState.selected)) {
                                  return Colors.white;
                                }
                                return null;
                              },
                            ),
                          ),
                        ),
                      ),

                    const SizedBox(height: 24),

                    // Add Application Section
                    if (_isConnected) ...[
                      const Text(
                        'Add Application',
                        style: TextStyle(
                          fontSize: 18,
                          fontWeight: FontWeight.bold,
                        ),
                      ),
                      const SizedBox(height: 12),
                      Card(
                        child: Padding(
                          padding: const EdgeInsets.all(16.0),
                          child: Row(
                            children: [
                              Expanded(
                                child: TextField(
                                  controller: _addAppController,
                                  decoration: const InputDecoration(
                                    hintText: 'Enter application name',
                                    prefixIcon: Icon(Icons.app_blocking),
                                    border: OutlineInputBorder(),
                                  ),
                                  onSubmitted: (_) => _addApplication(),
                                ),
                              ),
                              const SizedBox(width: 12),
                              ElevatedButton(
                                onPressed: _addApplication,
                                style: ElevatedButton.styleFrom(
                                  backgroundColor: const Color(0xFF2D3748),
                                  foregroundColor: Colors.white,
                                  padding: const EdgeInsets.all(16),
                                ),
                                child: const Icon(Icons.add),
                              ),
                            ],
                          ),
                        ),
                      ),

                      const SizedBox(height: 24),

                      // Applications List Header
                      Row(
                        mainAxisAlignment: MainAxisAlignment.spaceBetween,
                        children: [
                          const Text(
                            'Applications',
                            style: TextStyle(
                              fontSize: 18,
                              fontWeight: FontWeight.bold,
                            ),
                          ),
                          if (_blockedApps.isNotEmpty)
                            TextButton.icon(
                              onPressed: _resetApplications,
                              icon: const Icon(
                                Icons.clear_all,
                                color: Colors.red,
                              ),
                              label: const Text(
                                'Remove All',
                                style: TextStyle(color: Colors.red),
                              ),
                            ),
                        ],
                      ),
                      const SizedBox(height: 12),
                    ],

                    // Applications List - takes remaining space
                    Expanded(child: _buildApplicationsList()),
                  ],
                ),
              ),
            ),
    );
  }
}
