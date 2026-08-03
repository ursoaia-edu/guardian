import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../services/settings_service.dart';
import '../utils/snackbar_helper.dart';

/// The account's computers. A machine belongs to the account, not to a room —
/// moving it between rooms reinstalls nothing, it just receives a different
/// policy on its next sync.
class ComputersScreen extends StatefulWidget {
  const ComputersScreen({super.key});

  @override
  State<ComputersScreen> createState() => _ComputersScreenState();
}

class _ComputersScreenState extends State<ComputersScreen> {
  final SettingsService _service = SettingsService();

  List<Map<String, dynamic>> _computers = [];
  List<Map<String, dynamic>> _rooms = [];
  bool _loading = true;
  String? _error;
  Timer? _refresh;

  @override
  void initState() {
    super.initState();
    _service.addListener(_onServiceChanged);
    _load();
    _refresh = Timer.periodic(const Duration(seconds: 15), (_) => _load());
  }

  @override
  void dispose() {
    _service.removeListener(_onServiceChanged);
    _refresh?.cancel();
    super.dispose();
  }

  void _onServiceChanged() => _load();

  Future<void> _load() async {
    if (!mounted) return;
    try {
      final computers = await _service.computers();
      final rooms = await _service.rooms();
      if (!mounted) return;
      setState(() {
        _computers = computers;
        _rooms = rooms;
        _error = null;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.message;
        _computers = [];
      });
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _run(Future<void> Function() action, String success) async {
    try {
      await action();
      if (!mounted) return;
      showSnackBarMessage(
        context,
        message: success,
        backgroundColor: Colors.green,
      );
      await _load();
    } on ApiException catch (e) {
      if (!mounted) return;
      showSnackBarMessage(
        context,
        message: e.message,
        backgroundColor: Colors.red,
        duration: const Duration(seconds: 3),
      );
    }
  }

  /// A machine is "online" if it has synced within three poll intervals. The
  /// server records last_seen_at at most every 30 seconds, so anything much
  /// tighter than a minute would report healthy machines as missing.
  bool _isOnline(Map<String, dynamic> computer) {
    final raw = computer['last_seen_at'] as String?;
    if (raw == null) return false;
    final seen = DateTime.tryParse(raw);
    if (seen == null) return false;
    return DateTime.now().toUtc().difference(seen.toUtc()) <
        const Duration(seconds: 90);
  }

  String _nameOf(Map<String, dynamic> computer) {
    final display = (computer['display_name'] as String?) ?? '';
    if (display.isNotEmpty) return display;
    final hostname = (computer['hostname'] as String?) ?? '';
    return hostname.isNotEmpty ? hostname : 'Unnamed computer';
  }

  Future<void> _addComputer() async {
    String? token;
    String? error;
    try {
      token = await _service.createBindingToken();
    } on ApiException catch (e) {
      error = e.message;
    }
    if (!mounted) return;

    await showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Add a computer'),
        content: error != null
            ? Text(error)
            : Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text(
                    'Install the Guardian agent on the machine and put this token in its '
                    'agent.env as BINDING_TOKEN. The agent trades it for its own credential '
                    'on first run and then deletes it from the file.',
                  ),
                  const SizedBox(height: 12),
                  SelectableText(
                    token!,
                    style: const TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 12,
                    ),
                  ),
                ],
              ),
        actions: [
          if (error == null)
            TextButton(
              onPressed: () {
                Clipboard.setData(ClipboardData(text: token!));
                Navigator.pop(context);
              },
              child: const Text('Copy'),
            ),
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Close'),
          ),
        ],
      ),
    );
  }

  Future<void> _rename(Map<String, dynamic> computer) async {
    final controller = TextEditingController(
      text: (computer['display_name'] as String?) ?? '',
    );
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Rename computer'),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: InputDecoration(
            labelText: 'Name',
            hintText: (computer['hostname'] as String?) ?? '',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Save'),
          ),
        ],
      ),
    );
    final name = controller.text.trim();
    controller.dispose();
    if (ok != true) return;
    await _run(
      () => _service.updateComputer(computer['id'] as String, {
        'display_name': name,
      }),
      'Computer renamed',
    );
  }

  Future<void> _unenroll(Map<String, dynamic> computer) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Remove ${_nameOf(computer)}?'),
        content: const Text(
          'This revokes that machine\'s credential, so its agent stops syncing. It keeps '
          'enforcing the last policy it received until somebody reinstalls it, and it frees '
          'a seat on your plan.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    await _run(
      () => _service.deleteComputer(computer['id'] as String),
      'Computer removed',
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Computers'),
        actions: [
          IconButton(
            icon: const Icon(Icons.add),
            tooltip: 'Add a computer',
            onPressed: _addComputer,
          ),
          IconButton(icon: const Icon(Icons.refresh), onPressed: _load),
        ],
      ),
      body: RefreshIndicator(onRefresh: _load, child: _buildBody()),
    );
  }

  Widget _buildBody() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return ListView(
        children: [
          const SizedBox(height: 120),
          Icon(Icons.cloud_off, size: 56, color: Colors.grey.shade400),
          const SizedBox(height: 12),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 32),
            child: Text(_error!, textAlign: TextAlign.center),
          ),
        ],
      );
    }
    if (_computers.isEmpty) {
      return ListView(
        children: const [
          SizedBox(height: 120),
          Icon(Icons.computer, size: 56, color: Colors.grey),
          SizedBox(height: 12),
          Padding(
            padding: EdgeInsets.symmetric(horizontal: 32),
            child: Text(
              'No computers yet. Use + to get an installer token.',
              textAlign: TextAlign.center,
            ),
          ),
        ],
      );
    }

    return ListView.builder(
      padding: const EdgeInsets.all(12),
      itemCount: _computers.length,
      itemBuilder: (context, index) {
        final computer = _computers[index];
        final online = _isOnline(computer);
        final blocked = computer['blocked'] as bool? ?? false;
        final roomId = computer['room_id'] as String?;

        return Card(
          child: Column(
            children: [
              ListTile(
                leading: Icon(
                  Icons.circle,
                  size: 14,
                  color: online ? Colors.green : Colors.grey,
                ),
                title: Text(_nameOf(computer)),
                subtitle: Text(
                  [
                        computer['hostname'],
                        computer['os_name'],
                        if ((computer['agent_version'] as String?)
                                ?.isNotEmpty ??
                            false)
                          'agent ${computer['agent_version']}',
                      ]
                      .where((e) => e != null && (e as String).isNotEmpty)
                      .join(' · '),
                ),
                trailing: PopupMenuButton<String>(
                  onSelected: (choice) {
                    if (choice == 'rename') _rename(computer);
                    if (choice == 'remove') _unenroll(computer);
                  },
                  itemBuilder: (context) => const [
                    PopupMenuItem(value: 'rename', child: Text('Rename')),
                    PopupMenuItem(
                      value: 'remove',
                      child: Text('Remove from account'),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                child: Row(
                  children: [
                    const Icon(
                      Icons.meeting_room,
                      size: 18,
                      color: Colors.grey,
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: DropdownButton<String?>(
                        isExpanded: true,
                        value: _rooms.any((r) => r['id'] == roomId)
                            ? roomId
                            : null,
                        hint: const Text('No room — enforces nothing'),
                        items: [
                          const DropdownMenuItem<String?>(
                            value: null,
                            child: Text('No room — enforces nothing'),
                          ),
                          for (final room in _rooms)
                            DropdownMenuItem<String?>(
                              value: room['id'] as String,
                              child: Text(room['name'] as String),
                            ),
                        ],
                        onChanged: (value) => _run(
                          () => _service.updateComputer(
                            computer['id'] as String,
                            {'room_id': value},
                          ),
                          value == null ? 'Taken out of every room' : 'Moved',
                        ),
                      ),
                    ),
                  ],
                ),
              ),
              SwitchListTile(
                dense: true,
                title: const Text('Locked'),
                subtitle: Text(
                  blocked
                      ? 'Allows nothing but the system processes the agent protects'
                      : 'Follows its room\'s policy',
                ),
                value: blocked,
                onChanged: (v) => _run(
                  () => _service.updateComputer(computer['id'] as String, {
                    'blocked': v,
                  }),
                  v ? 'Computer locked' : 'Computer unlocked',
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}
