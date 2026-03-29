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

  List<Map<String, dynamic>> _allApps = [];
  bool _serverEnabled = false;
  String _serverMode = 'blacklist';
  bool _isLoading = true;
  bool _isConnected = false;

  List<Map<String, dynamic>> get _filteredApps =>
      _allApps.where((app) => (app['mode'] ?? 'blacklist') == _serverMode).toList();

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
      final serverAddress = await _settingsService.getServerAddress();
      _isConnected = await _settingsService.testConnection(serverAddress);

      if (_isConnected) {
        final apps = await _settingsService.getBlockedApplications();
        final status = await _settingsService.getServerStatus();

        setState(() {
          _allApps = apps;
          _serverEnabled = status['enabled'] as bool;
          _serverMode = status['mode'] as String;
        });
      } else {
        setState(() {
          _allApps = [];
          _serverEnabled = false;
          _serverMode = 'blacklist';
        });
      }
    } catch (e) {
      setState(() {
        _isConnected = false;
        _allApps = [];
        _serverEnabled = false;
        _serverMode = 'blacklist';
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(seconds: 3),
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

  void _showAddAppSheet() {
    _addAppController.clear();
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (context) => Padding(
        padding: EdgeInsets.only(
          left: 16,
          right: 16,
          top: 24,
          bottom: MediaQuery.of(context).viewInsets.bottom + 24,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              'Add Application',
              style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _addAppController,
              autofocus: true,
              decoration: const InputDecoration(
                hintText: 'Process name (e.g. firefox)',
                prefixIcon: Icon(Icons.app_blocking),
                border: OutlineInputBorder(),
              ),
              onSubmitted: (_) {
                Navigator.pop(context);
                _addApplication();
              },
            ),
            const SizedBox(height: 12),
            Text(
              'Will be added as $_serverMode',
              style: TextStyle(fontSize: 13, color: _modeColor(_serverMode)),
            ),
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton(
                onPressed: () {
                  Navigator.pop(context);
                  _addApplication();
                },
                style: ElevatedButton.styleFrom(
                  backgroundColor: const Color(0xFF2D3748),
                  foregroundColor: Colors.white,
                  padding: const EdgeInsets.symmetric(vertical: 14),
                ),
                child: const Text('Add'),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _addApplication() async {
    final appName = _addAppController.text.trim();

    if (appName.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          duration: Duration(seconds: 2),
          content: Text('Please enter an application name'),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    if (_allApps.any((app) => app['name'] == appName && app['mode'] == _serverMode)) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          duration: Duration(seconds: 2),
          content: Text('Application is already in this mode'),
          backgroundColor: Colors.orange,
        ),
      );
      return;
    }

    final success = await _settingsService.addBlockedApplication(appName, mode: _serverMode);

    if (success) {
      final apps = await _settingsService.getBlockedApplications();
      setState(() {
        _allApps = apps;
      });
      _addAppController.clear();

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(seconds: 2),
            content: Text('Added "$appName" ($_serverMode)'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 3),
            content: Text('Failed to add application'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _removeApplication(String appName, {String? mode}) async {
    final success = await _settingsService.removeBlockedApplication(appName, mode: mode);

    if (success) {
      setState(() {
        _allApps.removeWhere((app) =>
            app['name'] == appName && (mode == null || app['mode'] == mode));
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(seconds: 2),
            content: Text('Removed "$appName"'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 3),
            content: Text('Failed to remove application'),
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
        final index = _allApps.indexWhere((app) => app['name'] == name);
        if (index != -1) {
          _allApps[index]['enabled'] = enabled;
        }
      });
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 3),
            content: Text('Failed to update application'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _resetApplications() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Remove All'),
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
            child: const Text('Remove All'),
          ),
        ],
      ),
    );

    if (confirmed == true) {
      final success = await _settingsService.resetBlockedApplications();

      if (success) {
        setState(() {
          _allApps.clear();
        });

        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              duration: Duration(seconds: 2),
              content: Text('All applications removed'),
              backgroundColor: Colors.green,
            ),
          );
        }
      } else {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(
              duration: Duration(seconds: 3),
              content: Text('Failed to reset applications'),
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
            duration: const Duration(seconds: 2),
            content: Text('Server ${newStatus ? 'enabled' : 'disabled'}'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 3),
            content: Text('Failed to update server status'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _setMode(String mode) async {
    if (mode == _serverMode) return;

    final success = await _settingsService.toggleServerStatus(_serverEnabled, mode: mode);

    if (success) {
      setState(() {
        _serverMode = mode;
      });
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 3),
            content: Text('Failed to change mode'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Color _modeColor(String mode) {
    return mode == 'whitelist' ? Colors.blue : Colors.deepOrange;
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

  Widget _buildEmptyState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.security, size: 72, color: Colors.green.shade300),
          const SizedBox(height: 16),
          const Text(
            'No Applications',
            style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          const Text(
            'Tap + to add an application',
            style: TextStyle(color: Colors.grey),
          ),
        ],
      ),
    );
  }

  Widget _buildAppList() {
    final apps = _filteredApps;
    return ListView.builder(
      itemCount: apps.length,
      itemBuilder: (context, index) {
        final app = apps[index];
        final appName = app['name'] as String;
        final enabled = app['enabled'] as bool;
        final mode = (app['mode'] as String?) ?? 'blacklist';
        return Dismissible(
          key: Key('$appName:$mode'),
          direction: DismissDirection.endToStart,
          confirmDismiss: (_) async {
            return await showDialog<bool>(
              context: context,
              builder: (context) => AlertDialog(
                title: Text('Remove "$appName"?'),
                actions: [
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(false),
                    child: const Text('Cancel'),
                  ),
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(true),
                    style: TextButton.styleFrom(foregroundColor: Colors.red),
                    child: const Text('Remove'),
                  ),
                ],
              ),
            );
          },
          onDismissed: (_) => _removeApplication(appName, mode: mode),
          background: Container(
            alignment: Alignment.centerRight,
            padding: const EdgeInsets.only(right: 20),
            color: Colors.red,
            child: const Icon(Icons.delete, color: Colors.white),
          ),
          child: Card(
            child: ListTile(
              leading: Icon(
                mode == 'whitelist' ? Icons.check_circle_outline : Icons.block,
                color: _modeColor(mode),
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
        title: const Text('Dashboard'),
        actions: [
          if (_isConnected && _filteredApps.isNotEmpty)
            IconButton(
              onPressed: _resetApplications,
              icon: const Icon(Icons.clear_all),
              tooltip: 'Remove all',
            ),
          IconButton(onPressed: _loadData, icon: const Icon(Icons.refresh)),
        ],
      ),
      floatingActionButton: _isConnected
          ? FloatingActionButton(
              onPressed: _showAddAppSheet,
              backgroundColor: const Color(0xFF2D3748),
              foregroundColor: Colors.white,
              child: const Icon(Icons.add),
            )
          : null,
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : !_isConnected
              ? _buildDisconnected()
              : RefreshIndicator(
                  onRefresh: _loadData,
                  child: Column(
                    children: [
                      // Compact status bar
                      Container(
                        width: double.infinity,
                        padding: const EdgeInsets.symmetric(
                          horizontal: 16,
                          vertical: 10,
                        ),
                        color: _serverEnabled
                            ? Colors.green.shade50
                            : Colors.orange.shade50,
                        child: Row(
                          children: [
                            Icon(
                              _serverEnabled
                                  ? Icons.shield
                                  : Icons.shield_outlined,
                              size: 18,
                              color: _serverEnabled
                                  ? Colors.green.shade700
                                  : Colors.orange.shade700,
                            ),
                            const SizedBox(width: 8),
                            Text(
                              _serverEnabled ? 'Active' : 'Inactive',
                              style: TextStyle(
                                fontSize: 14,
                                fontWeight: FontWeight.w600,
                                color: _serverEnabled
                                    ? Colors.green.shade700
                                    : Colors.orange.shade700,
                              ),
                            ),
                            const Spacer(),
                            Switch(
                              value: _serverEnabled,
                              onChanged: (_) => _toggleServerStatus(),
                              materialTapTargetSize:
                                  MaterialTapTargetSize.shrinkWrap,
                            ),
                          ],
                        ),
                      ),

                      // Mode toggle
                      Padding(
                        padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
                        child: SizedBox(
                          width: double.infinity,
                          child: SegmentedButton<String>(
                            segments: [
                              ButtonSegment<String>(
                                value: 'blacklist',
                                label: const Text('Blacklist'),
                                icon: Icon(
                                  Icons.block,
                                  size: 18,
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
                                  size: 18,
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
                              backgroundColor:
                                  WidgetStateProperty.resolveWith(
                                (states) {
                                  if (states
                                      .contains(WidgetState.selected)) {
                                    return _serverMode == 'blacklist'
                                        ? Colors.deepOrange
                                        : Colors.blue;
                                  }
                                  return null;
                                },
                              ),
                              foregroundColor:
                                  WidgetStateProperty.resolveWith(
                                (states) {
                                  if (states
                                      .contains(WidgetState.selected)) {
                                    return Colors.white;
                                  }
                                  return null;
                                },
                              ),
                            ),
                          ),
                        ),
                      ),

                      // App count header
                      Padding(
                        padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
                        child: Row(
                          children: [
                            Text(
                              '${_filteredApps.length} application${_filteredApps.length != 1 ? 's' : ''}',
                              style: TextStyle(
                                fontSize: 13,
                                color: Colors.grey.shade600,
                              ),
                            ),
                          ],
                        ),
                      ),

                      // App list or empty state
                      Expanded(
                        child: _filteredApps.isEmpty
                            ? _buildEmptyState()
                            : _buildAppList(),
                      ),
                    ],
                  ),
                ),
    );
  }
}
