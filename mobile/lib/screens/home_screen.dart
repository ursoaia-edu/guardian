import 'package:flutter/material.dart';

import '../services/settings_service.dart';
import '../utils/snackbar_helper.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  final SettingsService _settingsService = SettingsService();
  final TextEditingController _addAppController = TextEditingController();

  List<Map<String, dynamic>> _allApps = [];
  String _serverMode = 'blacklist';
  bool _isLoading = true;
  bool _isConnected = false;

  List<Map<String, dynamic>> get _filteredApps =>
      _allApps.where((app) => (app['mode'] ?? 'blacklist') == _serverMode).toList()
        ..sort((a, b) {
          final aEnabled = a['enabled'] as bool;
          final bEnabled = b['enabled'] as bool;
          if (aEnabled != bEnabled) return aEnabled ? 1 : -1;
          return (a['name'] as String).compareTo(b['name'] as String);
        });

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

  Future<void> _loadData() async {
    setState(() {
      _isLoading = true;
    });

    try {
      final serverAddress = await _settingsService.getServerAddress();
      _isConnected = await _settingsService.testConnection(serverAddress);

      if (_isConnected) {
        final results = await Future.wait([
          _settingsService.getBlockedApplications(),
          _settingsService.getServerStatus(),
        ]);

        setState(() {
          _allApps = results[0] as List<Map<String, dynamic>>;
          final status = results[1] as Map<String, dynamic>;
          _serverMode = status['mode'] as String;
        });
      } else {
        setState(() {
          _allApps = [];
          _serverMode = 'blacklist';
        });
      }
    } catch (e) {
      setState(() {
        _isConnected = false;
        _allApps = [];
        _serverMode = 'blacklist';
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
      showSnackBarMessage(
        context,
        message: 'Please enter an application name',
        backgroundColor: Colors.orange,
      );
      return;
    }

    if (_allApps.any((app) => app['name'] == appName && app['mode'] == _serverMode)) {
      showSnackBarMessage(
        context,
        message: 'Application is already in this mode',
        backgroundColor: Colors.orange,
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

    } else {
      if (mounted) {
        showSnackBarMessage(
          context,
          message: 'Failed to add application',
          backgroundColor: Colors.red,
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
        showSnackBarMessage(
          context,
          message: 'Removed "$appName"',
          backgroundColor: Colors.green,
        );
      }
    } else {
      if (mounted) {
        showSnackBarMessage(
          context,
          message: 'Failed to remove application',
          backgroundColor: Colors.red,

        );
      }
    }
  }

  Future<void> _updateApplicationStatus(String name, bool enabled, {String? mode}) async {
    final success = await _settingsService.updateApplicationStatus(
      name,
      enabled,
      mode: mode,
    );

    if (success) {
      setState(() {
        final index = _allApps.indexWhere((app) => app['name'] == name && (mode == null || app['mode'] == mode));
        if (index != -1) {
          _allApps[index]['enabled'] = enabled;
        }
      });
    } else {
      if (mounted) {
        showSnackBarMessage(
          context,
          message: 'Failed to update application',
          backgroundColor: Colors.red,

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
          showSnackBarMessage(
            context,
            message: 'All applications removed',
            backgroundColor: Colors.green,
          );
        }
      } else {
        if (mounted) {
          showSnackBarMessage(
            context,
            message: 'Failed to reset applications',
            backgroundColor: Colors.red,
  
          );
        }
      }
    }
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

  Future<void> _setMode(String mode) async {
    if (mode == _serverMode) return;

    final success = await _settingsService.toggleServerStatus(_settingsService.serverEnabled, mode: mode);

    if (success) {
      setState(() {
        _serverMode = mode;
      });
    } else {
      if (mounted) {
        showSnackBarMessage(
          context,
          message: 'Failed to change mode',
          backgroundColor: Colors.red,

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
      padding: const EdgeInsets.only(bottom: 80),
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
                    _updateApplicationStatus(appName, newValue, mode: mode),
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
    _settingsService.serverEnabledNotifier.removeListener(_onServerEnabledChanged);
    _addAppController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Dashboard'),
        actions: [
          if (_isConnected)
            PopupMenuButton<String>(
              onSelected: (value) {
                if (value == 'remove_all') _resetApplications();
              },
              itemBuilder: (context) => [
                const PopupMenuItem(
                  value: 'remove_all',
                  child: Text('Remove All'),
                ),
              ],
            ),
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
