import 'dart:async';

import 'package:flutter/material.dart';

import '../services/settings_service.dart';

class ComputersScreen extends StatefulWidget {
  const ComputersScreen({super.key});

  @override
  State<ComputersScreen> createState() => _ComputersScreenState();
}

class _ComputersScreenState extends State<ComputersScreen> {
  final SettingsService _settingsService = SettingsService();

  List<Map<String, dynamic>> _computers = [];
  bool _isLoading = true;
  bool _isConnected = false;
  String _serverAddress = '';
  DateTime? _currentServerTime;
  Timer? _refreshTimer;

  @override
  void initState() {
    super.initState();
    _loadData();
    // Update server time every 60 seconds (lightweight, no full data fetch)
    _refreshTimer = Timer.periodic(const Duration(seconds: 10), (_) {
      _updateServerTime();
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
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
        // Load computers data
        final result = await _settingsService.getComputersData();
        setState(() {
          _computers = result['computers'] ?? [];
          _currentServerTime = result['current_time'];
        });
      } else {
        setState(() {
          _computers = [];
          _currentServerTime = null;
        });
      }
    } catch (e) {
      setState(() {
        _isConnected = false;
        _computers = [];
        _currentServerTime = null;
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Error loading computers: $e'),
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

  Future<void> _updateServerTime() async {
    try {
      final result = await _settingsService.getComputersData();
      final newComputers = result['computers'] ?? [];
      final newTime = result['current_time'];

      if (mounted && newTime != null) {
        setState(() {
          _computers = List<Map<String, dynamic>>.from(newComputers);
          _currentServerTime = newTime;
        });
      }
    } catch (e) {
      // Silently fail on time update
    }
  }

  Future<void> _toggleComputerBlocked(
    int computerId,
    bool currentBlocked,
  ) async {
    final newBlocked = !currentBlocked;

    final success = await _settingsService.updateComputerBlocked(
      computerId,
      newBlocked,
    );

    if (success) {
      // Update local state
      setState(() {
        final computerIndex = _computers.indexWhere(
          (computer) => computer['identity'] == computerId,
        );
        if (computerIndex != -1) {
          _computers[computerIndex]['blocked'] = newBlocked;
        }
      });

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            duration: const Duration(milliseconds: 500),
            content: Text('Computer ${newBlocked ? 'blocked' : 'unblocked'}'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(milliseconds: 500),
            content: Text('Failed to update computer status'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _resetAllComputers() async {
    final success = await _settingsService.resetAllComputers();

    if (success) {
      // Update computers data without loading spinner
      await _updateServerTime();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(milliseconds: 500),
            content: Text('All computers reset successfully'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(milliseconds: 500),
            content: Text('Failed to reset computers'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  bool _isComputerOnline(String datetimeStr) {
    if (_currentServerTime == null) return false;

    try {
      final computerTime = DateTime.parse(datetimeStr);
      final difference = _currentServerTime!
          .difference(computerTime)
          .inSeconds
          .abs();
      return difference < 60;
    } catch (e) {
      return false;
    }
  }

  String _formatLastSeen(String datetimeStr) {
    try {
      final computerTime = DateTime.parse(datetimeStr);
      final localTime = computerTime.toLocal();

      // Format: "Dec 27, 1:18 AM"
      final hour = localTime.hour > 12
          ? localTime.hour - 12
          : (localTime.hour == 0 ? 12 : localTime.hour);
      final minute = localTime.minute.toString().padLeft(2, '0');
      final period = localTime.hour >= 12 ? 'PM' : 'AM';
      final monthAbbr = _monthAbbr(localTime.month);

      return '$monthAbbr ${localTime.day}, $hour:$minute $period';
    } catch (e) {
      return datetimeStr;
    }
  }

  String _monthAbbr(int month) {
    const months = [
      'Jan',
      'Feb',
      'Mar',
      'Apr',
      'May',
      'Jun',
      'Jul',
      'Aug',
      'Sep',
      'Oct',
      'Nov',
      'Dec',
    ];
    return months[month - 1];
  }

  Widget _buildComputersList() {
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

    if (_computers.isEmpty) {
      return Card(
        child: SizedBox(
          width: double.infinity,
          child: Padding(
            padding: const EdgeInsets.all(24.0),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Icon(Icons.computer, size: 64, color: Colors.blue.shade400),
                const SizedBox(height: 16),
                const Text(
                  'No Computers',
                  style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                ),
                const SizedBox(height: 8),
                const Text(
                  'No computers available on the server',
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
      itemCount: _computers.length,
      itemBuilder: (context, index) {
        final computer = _computers[index];
        final identity = computer['identity'] as int;
        final blocked = computer['blocked'] as bool;
        final datetime = computer['datetime'] as String;
        final isOnline = _isComputerOnline(datetime);

        return Card(
          margin: const EdgeInsets.symmetric(vertical: 8),
          child: Padding(
            padding: const EdgeInsets.all(16.0),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  mainAxisAlignment: MainAxisAlignment.spaceBetween,
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            'Computer #$identity',
                            style: const TextStyle(
                              fontSize: 16,
                              fontWeight: FontWeight.bold,
                            ),
                          ),
                          const SizedBox(height: 4),
                          Row(
                            children: [
                              Icon(
                                Icons.circle,
                                size: 12,
                                color: isOnline ? Colors.green : Colors.grey,
                              ),
                              const SizedBox(width: 6),
                              Text(
                                isOnline ? 'Online' : 'Offline',
                                style: TextStyle(
                                  fontSize: 14,
                                  color: isOnline
                                      ? Colors.green
                                      : Colors.grey.shade600,
                                  fontWeight: FontWeight.w500,
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 8),
                          Text(
                            'Last seen: ${_formatLastSeen(datetime)}',
                            style: TextStyle(
                              fontSize: 12,
                              color: Colors.grey.shade600,
                            ),
                          ),
                        ],
                      ),
                    ),
                    Column(
                      crossAxisAlignment: CrossAxisAlignment.end,
                      children: [
                        const Text(
                          'Block',
                          style: TextStyle(fontSize: 12, color: Colors.grey),
                        ),
                        const SizedBox(height: 4),
                        Switch(
                          value: blocked,
                          onChanged: (_) =>
                              _toggleComputerBlocked(identity, blocked),
                          inactiveThumbColor: Colors.grey,
                          inactiveTrackColor: Colors.grey.shade300,
                        ),
                      ],
                    ),
                  ],
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Computers'),
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
                  // Computers List Header
                  if (_isConnected) ...[
                    Row(
                      mainAxisAlignment: MainAxisAlignment.spaceBetween,
                      children: [
                        const Text(
                          'Computers',
                          style: TextStyle(
                            fontSize: 18,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                        Expanded(
                          child: Row(
                            mainAxisAlignment: MainAxisAlignment.end,
                            children: [
                              Text(
                                '${_computers.length} device${_computers.length != 1 ? 's' : ''}',
                                style: TextStyle(
                                  fontSize: 14,
                                  color: Colors.grey.shade600,
                                ),
                              ),
                              const SizedBox(width: 12),
                              ElevatedButton(
                                onPressed: _resetAllComputers,
                                style: ElevatedButton.styleFrom(
                                  padding: const EdgeInsets.symmetric(
                                    horizontal: 12,
                                    vertical: 8,
                                  ),
                                  backgroundColor: Colors.blue,
                                ),
                                child: const Text(
                                  'Reset All',
                                  style: TextStyle(
                                    fontSize: 12,
                                    color: Colors.white,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 12),
                  ],
                  // Computers List - takes remaining space
                  Expanded(child: _buildComputersList()),
                ],
              ),
            ),
    );
  }
}
