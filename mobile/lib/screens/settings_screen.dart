import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';

import '../services/settings_service.dart';
import '../utils/snackbar_helper.dart';

/// Where the server is, who is signed in, and which account is being acted in.
/// The token field that used to live here is gone: the app holds a session it
/// obtained by signing in, not a shared secret somebody pasted.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  final SettingsService _service = SettingsService();
  final TextEditingController _serverController = TextEditingController();

  List<Map<String, dynamic>> _accounts = [];
  String? _email;
  String _version = '';
  bool _loading = true;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _serverController.text = _service.serverAddress;
    _load();
  }

  @override
  void dispose() {
    _serverController.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final info = await PackageInfo.fromPlatform();
      final identity = await _service.me();
      if (!mounted) return;
      setState(() {
        _version = info.version;
        _email = identity['email'] as String?;
        _accounts =
            (identity['accounts'] as List?)
                ?.map((e) => Map<String, dynamic>.from(e as Map))
                .toList() ??
            [];
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _saveServer() async {
    final address = _serverController.text.trim();
    if (!address.startsWith('http://') && !address.startsWith('https://')) {
      showSnackBarMessage(
        context,
        message: 'The address must start with http:// or https://',
        backgroundColor: Colors.orange,
      );
      return;
    }
    setState(() => _busy = true);
    final reachable = await _service.testConnection(address);
    await _service.setServerAddress(address);
    if (!mounted) return;
    setState(() => _busy = false);
    showSnackBarMessage(
      context,
      message: reachable
          ? 'Saved — the server answered'
          : 'Saved, but the server did not answer',
      backgroundColor: reachable ? Colors.green : Colors.orange,
      duration: const Duration(seconds: 2),
    );
  }

  Future<void> _signOut() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Sign out?'),
        content: const Text(
          'This ends the session on the server, so this phone stops having access. '
          'Your computers keep enforcing their policy.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Sign out'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    await _service.signOut();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Settings'),
        actions: [
          IconButton(icon: const Icon(Icons.refresh), onPressed: _load),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(12),
        children: [
          Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'Server',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _serverController,
                    decoration: const InputDecoration(
                      labelText: 'Server address',
                      border: OutlineInputBorder(),
                    ),
                    keyboardType: TextInputType.url,
                    autocorrect: false,
                  ),
                  const SizedBox(height: 12),
                  Align(
                    alignment: Alignment.centerRight,
                    child: FilledButton(
                      onPressed: _busy ? null : _saveServer,
                      child: const Text('Save and test'),
                    ),
                  ),
                ],
              ),
            ),
          ),
          Card(
            child: Column(
              children: [
                ListTile(
                  leading: const Icon(Icons.person),
                  title: Text(_email ?? 'Signed in'),
                  subtitle: _error == null
                      ? null
                      : Text(
                          _error!,
                          style: const TextStyle(color: Colors.red),
                        ),
                ),
                if (_loading) const LinearProgressIndicator(),
                if (_accounts.length > 1)
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                    child: DropdownButton<String>(
                      isExpanded: true,
                      value:
                          _accounts.any(
                            (a) => a['account_id'] == _service.accountId,
                          )
                          ? _service.accountId
                          : null,
                      hint: const Text('Choose an account'),
                      items: [
                        for (final account in _accounts)
                          DropdownMenuItem(
                            value: account['account_id'] as String,
                            child: Text(
                              '${account['name'] ?? 'Account'} · ${account['role']}',
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                      ],
                      onChanged: (id) async {
                        // The messenger is taken before the await: the context
                        // this closure captured may be gone by the time the
                        // account switch finishes.
                        final messenger = ScaffoldMessenger.of(context);
                        await _service.setAccountId(id);
                        messenger.showSnackBar(
                          const SnackBar(
                            content: Text('Switched account'),
                            backgroundColor: Colors.green,
                            duration: Duration(seconds: 1),
                          ),
                        );
                      },
                    ),
                  ),
                ListTile(
                  leading: const Icon(Icons.logout, color: Colors.red),
                  title: const Text(
                    'Sign out',
                    style: TextStyle(color: Colors.red),
                  ),
                  onTap: _signOut,
                ),
              ],
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(16),
            child: Text(
              _version.isEmpty ? '' : 'Guardian $_version',
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ),
        ],
      ),
    );
  }
}
