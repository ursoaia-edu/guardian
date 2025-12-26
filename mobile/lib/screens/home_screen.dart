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

  List<String> _blockedApps = [];
  bool _serverEnabled = false;
  String _serverAddress = '';
  bool _isLoading = true;
  bool _isConnected = false;

  @override
  void initState() {
    super.initState();
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
          _serverEnabled = status;
        });
      } else {
        setState(() {
          _blockedApps = [];
          _serverEnabled = false;
        });
      }
    } catch (e) {
      setState(() {
        _isConnected = false;
        _blockedApps = [];
        _serverEnabled = false;
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

    if (_blockedApps.contains(appName)) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          duration: Duration(milliseconds: 500),
          content: Text('Application is already in the blocked list'),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    final success = await _settingsService.addBlockedApplication(appName);

    if (success) {
      setState(() {
        _blockedApps.add(appName);
      });
      _addAppController.clear();

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('Added "$appName" to blocked list'),
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
        _blockedApps.remove(appName);
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('Removed "$appName" from blocked list'),
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

  Future<void> _resetApplications() async {
    // Show confirmation dialog
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Reset Blocked Applications'),
        content: const Text(
          'Are you sure you want to remove all blocked applications?',
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
              content: Text('All blocked applications removed'),
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
                  'No Blocked Applications',
                  style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                ),
                const SizedBox(height: 8),
                const Text(
                  'Add applications to monitor and block',
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
        final appName = _blockedApps[index];
        return Card(
          child: ListTile(
            leading: IconButton(
              onPressed: () => _removeApplication(appName),
              icon: const Icon(Icons.delete, color: Colors.red),
              tooltip: 'Remove from blocked list',
            ),
            title: Text(appName),
            trailing: Switch(
              value: true,
              onChanged: (_) {},
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
          : Padding(
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

                  const SizedBox(height: 24),

                  // Add Application Section
                  if (_isConnected) ...[
                    const Text(
                      'Add Blocked Application',
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

                    // Blocked Applications List
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        const Text(
                          'Blocked Applications',
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
                              'Reset All',
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
    );
  }
}
