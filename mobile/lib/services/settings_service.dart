import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

/// Raised for anything the server refused or could not answer. The old client
/// swallowed every failure and returned `false` or an empty list, so an outage,
/// an expired session and "there is nothing here" were indistinguishable — on
/// screen and in the code. They are different things and the UI says so.
class ApiException implements Exception {
  ApiException(this.message, {this.statusCode});

  final String message;
  final int? statusCode;

  bool get isUnauthorized => statusCode == 401;
  bool get isForbidden => statusCode == 403;

  @override
  String toString() => message;
}

/// The Guardian API client and the little state that goes with it: where the
/// server is, the session token, which account is being acted in, and which
/// room is on screen.
///
/// Session auth, not a shared admin token. The app signs in as a person and
/// sends `Authorization: Bearer <session token>`; login asks for
/// `"client": "mobile"` because a browser deliberately gets the token only in
/// an HttpOnly cookie it cannot read.
class SettingsService extends ChangeNotifier {
  static final SettingsService _instance = SettingsService._internal();
  factory SettingsService() => _instance;
  SettingsService._internal();

  static const String _serverAddressKey = 'server_address';
  static const String _tokenKey = 'session_token';
  static const String _accountKey = 'account_id';
  static const String _roomKey = 'room_id';
  static const String _defaultServerAddress = 'http://192.168.1.10:8080';

  static const Duration _timeout = Duration(seconds: 10);

  /// How an HTTP client is obtained. Overridden by tests with a mock, which is
  /// the only reason it is not simply `http.Client()` inline: the API contract
  /// this class encodes — the Bearer header, X-Guardian-Account, the shape of
  /// an error — is worth testing without a server.
  @visibleForTesting
  http.Client Function() clientFactory = http.Client.new;

  /// Puts the service straight into a signed-in state, skipping the login
  /// round trip. Tests only.
  @visibleForTesting
  Future<void> signInForTest(String token, String accountId) async {
    await _setToken(token);
    await setAccountId(accountId);
  }

  /// Drops everything read from storage. Tests only: the singleton otherwise
  /// carries one test's session into the next.
  @visibleForTesting
  void resetForTest() {
    _loaded = false;
    _token = null;
    _accountId = null;
    _roomId = null;
    _serverAddress = _defaultServerAddress;
  }

  String _serverAddress = _defaultServerAddress;
  String? _token;
  String? _accountId;
  String? _roomId;
  bool _loaded = false;

  String get serverAddress => _serverAddress;
  String? get accountId => _accountId;
  String? get roomId => _roomId;
  bool get isSignedIn => _token != null && _token!.isNotEmpty;

  /// Reads the stored state once, at startup. Everything after this is
  /// synchronous, so a widget can ask whether it is signed in while building.
  Future<void> load() async {
    if (_loaded) return;
    final prefs = await SharedPreferences.getInstance();
    _serverAddress =
        prefs.getString(_serverAddressKey) ?? _defaultServerAddress;
    _token = prefs.getString(_tokenKey);
    _accountId = prefs.getString(_accountKey);
    _roomId = prefs.getString(_roomKey);
    _loaded = true;
    notifyListeners();
  }

  Future<void> setServerAddress(String address) async {
    final prefs = await SharedPreferences.getInstance();
    _serverAddress = address;
    await prefs.setString(_serverAddressKey, address);
    notifyListeners();
  }

  Future<void> _setToken(String? token) async {
    final prefs = await SharedPreferences.getInstance();
    _token = token;
    if (token == null || token.isEmpty) {
      await prefs.remove(_tokenKey);
    } else {
      await prefs.setString(_tokenKey, token);
    }
  }

  /// The account this session acts in. A user owns their own account and may
  /// also be an admin or a room guest of somebody else's, so which one is meant
  /// has to be said — see X-Guardian-Account in specs/api.md.
  Future<void> setAccountId(String? id) async {
    final prefs = await SharedPreferences.getInstance();
    _accountId = id;
    if (id == null) {
      await prefs.remove(_accountKey);
    } else {
      await prefs.setString(_accountKey, id);
    }
    // Rooms belong to an account, so the selected one cannot survive a switch.
    await setRoomId(null);
    notifyListeners();
  }

  Future<void> setRoomId(String? id) async {
    final prefs = await SharedPreferences.getInstance();
    _roomId = id;
    if (id == null) {
      await prefs.remove(_roomKey);
    } else {
      await prefs.setString(_roomKey, id);
    }
    notifyListeners();
  }

  Map<String, String> _headers({bool json = true}) {
    final headers = <String, String>{'Accept': 'application/json'};
    if (json) headers['Content-Type'] = 'application/json';
    if (isSignedIn) headers['Authorization'] = 'Bearer $_token';
    if (_accountId != null) headers['X-Guardian-Account'] = _accountId!;
    return headers;
  }

  Future<dynamic> _send(
    String method,
    String path, {
    Object? body,
    bool authenticated = true,
  }) async {
    final client = clientFactory();
    try {
      final uri = Uri.parse('$_serverAddress$path');
      final headers = _headers();
      if (!authenticated) {
        headers.remove('Authorization');
        headers.remove('X-Guardian-Account');
      }
      final encoded = body == null ? null : json.encode(body);

      late http.Response response;
      switch (method) {
        case 'GET':
          response = await client.get(uri, headers: headers).timeout(_timeout);
        case 'POST':
          response = await client
              .post(uri, headers: headers, body: encoded)
              .timeout(_timeout);
        case 'PATCH':
          response = await client
              .patch(uri, headers: headers, body: encoded)
              .timeout(_timeout);
        case 'DELETE':
          response = await client
              .delete(uri, headers: headers, body: encoded)
              .timeout(_timeout);
        default:
          throw ArgumentError('unsupported method $method');
      }

      if (response.statusCode == 204 || response.body.isEmpty) {
        if (response.statusCode >= 400) {
          throw ApiException(
            _messageFor(response.statusCode, null),
            statusCode: response.statusCode,
          );
        }
        return null;
      }

      dynamic decoded;
      try {
        decoded = json.decode(response.body);
      } catch (_) {
        if (response.statusCode >= 400) {
          throw ApiException(
            _messageFor(response.statusCode, null),
            statusCode: response.statusCode,
          );
        }
        throw ApiException('The server sent a reply this app could not read.');
      }

      if (response.statusCode >= 400) {
        final serverSaid = decoded is Map && decoded['error'] is String
            ? decoded['error'] as String
            : null;
        throw ApiException(
          _messageFor(response.statusCode, serverSaid),
          statusCode: response.statusCode,
        );
      }
      return decoded;
    } on ApiException {
      rethrow;
    } on TimeoutException {
      throw ApiException('The server did not answer in time.');
    } catch (e) {
      throw ApiException('Could not reach the server at $_serverAddress.');
    } finally {
      client.close();
    }
  }

  String _messageFor(int status, String? serverSaid) {
    if (serverSaid != null && serverSaid.isNotEmpty) return serverSaid;
    switch (status) {
      case 401:
        return 'Please sign in again.';
      case 403:
        return 'Only account admins can do that.';
      case 404:
        return 'Not found.';
      case 402:
        return 'Your plan does not cover that.';
      default:
        return 'The server returned an error ($status).';
    }
  }

  Future<bool> testConnection([String? address]) async {
    final client = clientFactory();
    try {
      final uri = Uri.parse('${address ?? _serverAddress}/health');
      final response = await client.get(uri).timeout(_timeout);
      return response.statusCode == 200;
    } catch (_) {
      return false;
    } finally {
      client.close();
    }
  }

  // ---------------------------------------------------------------- accounts

  Future<void> signIn(String email, String password) async {
    // "client": "mobile" is what asks for the token in the body; without it the
    // server answers with the cookie alone, which this app cannot use.
    final body = await _send(
      'POST',
      '/api/v1/auth/login',
      authenticated: false,
      body: {'email': email, 'password': password, 'client': 'mobile'},
    );
    final token = body is Map ? body['token'] as String? : null;
    if (token == null || token.isEmpty) {
      throw ApiException('The server did not return a session token.');
    }
    await _setToken(token);
    _accountId = null;

    // Land in an account straight away: the first one is the strongest role
    // the user holds, which is their own account when they have one.
    final identity = await me();
    final accounts = (identity['accounts'] as List?) ?? const [];
    if (accounts.isNotEmpty) {
      await setAccountId((accounts.first as Map)['account_id'] as String?);
    }
    notifyListeners();
  }

  Future<void> register(String email, String password, String name) async {
    await _send(
      'POST',
      '/api/v1/auth/register',
      authenticated: false,
      body: {'email': email, 'password': password, 'name': name},
    );
  }

  Future<void> signOut() async {
    try {
      await _send('POST', '/api/v1/auth/logout');
    } on ApiException {
      // The session may already be gone server-side; signing out locally is
      // still the right outcome and never fails.
    }
    await _setToken(null);
    await setAccountId(null);
    notifyListeners();
  }

  Future<Map<String, dynamic>> me() async {
    final body = await _send('GET', '/api/v1/me');
    return Map<String, dynamic>.from(body as Map);
  }

  Future<List<Map<String, dynamic>>> accounts() async {
    final identity = await me();
    return _asMaps(identity['accounts']);
  }

  // ------------------------------------------------------------------- rooms

  Future<List<Map<String, dynamic>>> rooms() async =>
      _asMaps((await _send('GET', '/api/v1/rooms') as Map)['rooms']);

  Future<Map<String, dynamic>> createRoom(String name) async =>
      Map<String, dynamic>.from(
        await _send('POST', '/api/v1/rooms', body: {'name': name}) as Map,
      );

  Future<Map<String, dynamic>> updateRoom(
    String roomId,
    Map<String, dynamic> changes,
  ) async => Map<String, dynamic>.from(
    await _send('PATCH', '/api/v1/rooms/$roomId', body: changes) as Map,
  );

  Future<void> deleteRoom(String roomId) =>
      _send('DELETE', '/api/v1/rooms/$roomId');

  // ------------------------------------------------------------ applications

  Future<List<Map<String, dynamic>>> applications(String roomId) async =>
      _asMaps(
        (await _send('GET', '/api/v1/rooms/$roomId/applications')
            as Map)['applications'],
      );

  Future<void> addApplication(String roomId, String name, String list) => _send(
    'POST',
    '/api/v1/rooms/$roomId/applications',
    body: {'name': name, 'list': list},
  );

  /// Switches a rule off without losing it — /agent/sync stops sending a
  /// disabled entry, and turning it back on restores it unchanged.
  Future<void> setApplicationEnabled(
    String roomId,
    String appId,
    bool enabled,
  ) => _send(
    'PATCH',
    '/api/v1/rooms/$roomId/applications/$appId',
    body: {'enabled': enabled},
  );

  Future<void> deleteApplication(String roomId, String appId) =>
      _send('DELETE', '/api/v1/rooms/$roomId/applications/$appId');

  // --------------------------------------------------------------- computers

  Future<List<Map<String, dynamic>>> computers() async =>
      _asMaps((await _send('GET', '/api/v1/computers') as Map)['computers']);

  Future<void> updateComputer(
    String computerId,
    Map<String, dynamic> changes,
  ) => _send('PATCH', '/api/v1/computers/$computerId', body: changes);

  /// Unenrols a machine: it revokes that one agent's token. The machine keeps
  /// enforcing its last policy until somebody reinstalls it.
  Future<void> deleteComputer(String computerId) =>
      _send('DELETE', '/api/v1/computers/$computerId');

  // ------------------------------------------------------------------ events

  Future<List<Map<String, dynamic>>> events({int limit = 50}) async => _asMaps(
    (await _send('GET', '/api/v1/events?limit=$limit') as Map)['events'],
  );

  // --------------------------------------------------------- binding tokens

  Future<String> createBindingToken() async {
    final body = await _send('POST', '/api/v1/binding-tokens');
    return (body as Map)['token'] as String;
  }

  List<Map<String, dynamic>> _asMaps(dynamic value) {
    if (value is! List) return const [];
    return value.map((e) => Map<String, dynamic>.from(e as Map)).toList();
  }
}
