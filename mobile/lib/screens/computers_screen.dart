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
  DateTime? _currentServerTime;
  Timer? _refreshTimer;

  @override
  void initState() {
    super.initState();
    _settingsService.addListener(_onSettingsChanged);
    _loadData();
    _refreshTimer = Timer.periodic(const Duration(seconds: 10), (_) {
      _updateServerTime();
    });
  }

  void _onSettingsChanged() {
    _loadData();
  }

  @override
  void dispose() {
    _settingsService.removeListener(_onSettingsChanged);
    _refreshTimer?.cancel();
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
            duration: const Duration(seconds: 3),
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
      // Silently fail on background update
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
            duration: const Duration(seconds: 2),
            content: Text(
              'Computer #$computerId ${newBlocked ? 'blocked' : 'unblocked'}',
            ),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 3),
            content: Text('Failed to update computer status'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _unblockAllComputers() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Unblock All?'),
        content: const Text('This will unblock all computers.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Unblock All'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    final success = await _settingsService.resetAllComputers();

    if (success) {
      await _updateServerTime();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 2),
            content: Text('All computers unblocked'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 3),
            content: Text('Failed to unblock computers'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _blockAllComputers() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Block All?'),
        content: const Text('This will block all computers.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            style: TextButton.styleFrom(foregroundColor: Colors.red),
            child: const Text('Block All'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    final success = await _settingsService.blockAllComputers();

    if (success) {
      await _updateServerTime();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 2),
            content: Text('All computers blocked'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } else {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            duration: Duration(seconds: 3),
            content: Text('Failed to block computers'),
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
      'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
      'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
    ];
    return months[month - 1];
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
          Icon(Icons.computer, size: 72, color: Colors.grey.shade300),
          const SizedBox(height: 16),
          const Text(
            'No Computers',
            style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          const Text(
            'No computers have connected yet',
            textAlign: TextAlign.center,
            style: TextStyle(color: Colors.grey),
          ),
        ],
      ),
    );
  }

  Widget _buildComputersList() {
    return ListView.builder(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      itemCount: _computers.length,
      itemBuilder: (context, index) {
        final computer = _computers[index];
        final identity = computer['identity'] as int;
        final blocked = computer['blocked'] as bool;
        final datetime = computer['datetime'] as String;
        final isOnline = _isComputerOnline(datetime);

        return Card(
          margin: const EdgeInsets.symmetric(vertical: 6),
          child: Padding(
            padding: const EdgeInsets.all(14.0),
            child: Row(
              children: [
                // Online indicator
                Container(
                  width: 10,
                  height: 10,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: isOnline ? Colors.green : Colors.grey.shade400,
                  ),
                ),
                const SizedBox(width: 14),
                // Info
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        'Computer #$identity',
                        style: const TextStyle(
                          fontSize: 15,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const SizedBox(height: 2),
                      Text(
                        isOnline
                            ? 'Online'
                            : 'Last seen ${_formatLastSeen(datetime)}',
                        style: TextStyle(
                          fontSize: 12,
                          color: isOnline
                              ? Colors.green
                              : Colors.grey.shade600,
                        ),
                      ),
                    ],
                  ),
                ),
                // Block switch
                Switch(
                  value: blocked,
                  onChanged: (_) =>
                      _toggleComputerBlocked(identity, blocked),
                  inactiveThumbColor: Colors.grey,
                  inactiveTrackColor: Colors.grey.shade300,
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
          if (_isConnected && _computers.isNotEmpty) ...[
            IconButton(
              onPressed: _blockAllComputers,
              icon: const Icon(Icons.lock),
              tooltip: 'Block all',
            ),
            IconButton(
              onPressed: _unblockAllComputers,
              icon: const Icon(Icons.lock_open),
              tooltip: 'Unblock all',
            ),
          ],
          IconButton(onPressed: _loadData, icon: const Icon(Icons.refresh)),
        ],
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : !_isConnected
              ? _buildDisconnected()
              : RefreshIndicator(
                  onRefresh: _loadData,
                  child: Column(
                    children: [
                      // Device count bar
                      if (_computers.isNotEmpty)
                        Container(
                          width: double.infinity,
                          padding: const EdgeInsets.symmetric(
                            horizontal: 16,
                            vertical: 8,
                          ),
                          color: Colors.grey.shade100,
                          child: Text(
                            '${_computers.length} device${_computers.length != 1 ? 's' : ''}'
                            ' \u2022 '
                            '${_computers.where((c) => _isComputerOnline(c['datetime'] as String)).length} online',
                            style: TextStyle(
                              fontSize: 13,
                              color: Colors.grey.shade700,
                            ),
                          ),
                        ),
                      Expanded(
                        child: _computers.isEmpty
                            ? ListView(children: [
                                SizedBox(
                                  height:
                                      MediaQuery.of(context).size.height * 0.5,
                                  child: _buildEmpty(),
                                ),
                              ])
                            : _buildComputersList(),
                      ),
                    ],
                  ),
                ),
    );
  }
}
