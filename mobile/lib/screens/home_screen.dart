import 'package:flutter/material.dart';

import '../services/settings_service.dart';
import '../utils/snackbar_helper.dart';
import '../widgets/room_selector.dart';

/// The rules of one room. Policy is per-room now, not one global list, so this
/// screen is always about the room named in the app bar.
class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  final SettingsService _service = SettingsService();

  List<Map<String, dynamic>> _rooms = [];
  List<Map<String, dynamic>> _apps = [];
  Map<String, dynamic>? _room;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _service.addListener(_onServiceChanged);
    _load();
  }

  @override
  void dispose() {
    _service.removeListener(_onServiceChanged);
    super.dispose();
  }

  void _onServiceChanged() => _load();

  Future<void> _load() async {
    if (!mounted) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rooms = await _service.rooms();
      final room = pickRoom(rooms, _service.roomId);
      final apps = room == null
          ? <Map<String, dynamic>>[]
          : await _service.applications(room['id'] as String);
      if (!mounted) return;
      setState(() {
        _rooms = rooms;
        _room = room;
        _apps = apps;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.message;
        _rooms = [];
        _apps = [];
        _room = null;
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

  Future<void> _addApplication() async {
    final room = _room;
    if (room == null) return;
    final controller = TextEditingController();
    var list = (room['mode'] as String?) ?? 'blacklist';

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('Add a rule'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: controller,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: 'Process name',
                  hintText: 'steam.exe',
                ),
              ),
              const SizedBox(height: 16),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(value: 'blacklist', label: Text('Blocked')),
                  ButtonSegment(value: 'whitelist', label: Text('Allowed')),
                ],
                selected: {list},
                onSelectionChanged: (s) => setDialogState(() => list = s.first),
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('Add'),
            ),
          ],
        ),
      ),
    );

    final name = controller.text.trim();
    controller.dispose();
    if (confirmed != true || name.isEmpty) return;
    await _run(
      () => _service.addApplication(room['id'] as String, name, list),
      'Rule added',
    );
  }

  @override
  Widget build(BuildContext context) {
    final room = _room;
    final mode = (room?['mode'] as String?) ?? 'blacklist';

    return Scaffold(
      appBar: AppBar(
        title: RoomSelector(
          rooms: _rooms,
          selected: room,
          onChanged: (r) async {
            await _service.setRoomId(r?['id'] as String?);
          },
        ),
        actions: [
          IconButton(icon: const Icon(Icons.refresh), onPressed: _load),
        ],
      ),
      floatingActionButton: room == null
          ? null
          : FloatingActionButton(
              onPressed: _addApplication,
              child: const Icon(Icons.add),
            ),
      body: RefreshIndicator(onRefresh: _load, child: _buildBody(room, mode)),
    );
  }

  Widget _buildBody(Map<String, dynamic>? room, String mode) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) return _message(Icons.cloud_off, _error!);
    if (room == null) {
      return _message(
        Icons.meeting_room_outlined,
        'No rooms yet. Create one on the Room tab, then put a computer in it.',
      );
    }

    final active = _apps.where((a) => a['list'] == mode).toList();
    final other = _apps.where((a) => a['list'] != mode).toList();
    if (_apps.isEmpty) {
      return _message(Icons.rule, 'No rules in ${room['name']} yet.');
    }

    return ListView(
      children: [
        if ((room['protection_enabled'] as bool?) != true)
          Card(
            margin: const EdgeInsets.all(12),
            color: Colors.orange.shade50,
            child: const ListTile(
              leading: Icon(Icons.shield_outlined, color: Colors.orange),
              title: Text('Protection is off for this room'),
              subtitle: Text('These rules are stored but nothing is enforced.'),
            ),
          ),
        ..._section(
          context,
          mode == 'whitelist' ? 'Allowed' : 'Blocked',
          active,
          room,
        ),
        if (other.isNotEmpty)
          ..._section(context, 'Inactive in this mode', other, room),
      ],
    );
  }

  List<Widget> _section(
    BuildContext context,
    String title,
    List<Map<String, dynamic>> apps,
    Map<String, dynamic> room,
  ) {
    if (apps.isEmpty) return const [];
    apps.sort((a, b) {
      final ae = a['enabled'] as bool? ?? true;
      final be = b['enabled'] as bool? ?? true;
      if (ae != be) return ae ? -1 : 1;
      return (a['name'] as String).compareTo(b['name'] as String);
    });
    return [
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
        child: Text(title, style: Theme.of(context).textTheme.labelLarge),
      ),
      for (final app in apps)
        ListTile(
          title: Text(app['name'] as String),
          subtitle: Text(
            (app['enabled'] as bool? ?? true) ? 'Enforced' : 'Switched off',
          ),
          leading: Switch(
            value: app['enabled'] as bool? ?? true,
            onChanged: (v) => _run(
              () => _service.setApplicationEnabled(
                room['id'] as String,
                app['id'] as String,
                v,
              ),
              v ? 'Rule switched on' : 'Rule switched off',
            ),
          ),
          trailing: IconButton(
            icon: const Icon(Icons.delete_outline),
            onPressed: () => _run(
              () => _service.deleteApplication(
                room['id'] as String,
                app['id'] as String,
              ),
              'Rule removed',
            ),
          ),
        ),
    ];
  }

  Widget _message(IconData icon, String text) => ListView(
    children: [
      const SizedBox(height: 120),
      Icon(icon, size: 56, color: Colors.grey),
      const SizedBox(height: 12),
      Padding(
        padding: const EdgeInsets.symmetric(horizontal: 32),
        child: Text(text, textAlign: TextAlign.center),
      ),
    ],
  );
}
