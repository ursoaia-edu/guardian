import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

class SettingsService extends ChangeNotifier {
  static final SettingsService _instance = SettingsService._internal();
  factory SettingsService() => _instance;
  SettingsService._internal();

  static const String _serverAddressKey = 'server_address';
  static const String _tokenKey = 'auth_token';
  static const String _defaultServerAddress = 'http://192.168.1.10:8080';

  /// Gets the server address from persistent storage
  Future<String> getServerAddress() async {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getString(_serverAddressKey) ?? _defaultServerAddress;
  }

  /// Sets the server address in persistent storage
  Future<void> setServerAddress(String address) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_serverAddressKey, address);
    notifyListeners();
  }

  /// Gets the authentication token from persistent storage
  Future<String?> getToken() async {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getString(_tokenKey);
  }

  /// Sets the authentication token in persistent storage
  Future<void> setToken(String? token) async {
    final prefs = await SharedPreferences.getInstance();
    if (token == null || token.isEmpty) {
      await prefs.remove(_tokenKey);
    } else {
      await prefs.setString(_tokenKey, token);
    }
    notifyListeners();
  }

  /// Gets HTTP headers with authorization token if available
  Future<Map<String, String>> _getHeaders({
    Map<String, String>? additionalHeaders,
  }) async {
    final headers = <String, String>{};

    final token = await getToken();
    if (token != null && token.isNotEmpty) {
      headers['Authorization'] = 'Bearer $token';
    }

    if (additionalHeaders != null) {
      headers.addAll(additionalHeaders);
    }

    return headers;
  }

  /// Tests connection to the server
  Future<bool> testConnection(String serverAddress) async {
    final client = http.Client();
    try {
      final uri = Uri.parse('$serverAddress/health');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      if (response.statusCode == 200) {
        try {
          final body = json.decode(response.body);
          return body['status'] == 'ok';
        } catch (_) {
          return true;
        }
      }

      return false;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Gets the list of blocked applications from the server
  Future<List<Map<String, dynamic>>> getBlockedApplications() async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      if (response.statusCode == 200) {
        final body = json.decode(response.body);
        final List<dynamic> applications = body['applications'] ?? [];
        return applications.cast<Map<String, dynamic>>();
      }

      return [];
    } catch (e) {
      return [];
    } finally {
      client.close();
    }
  }

  /// Gets the server status (enabled state and mode)
  Future<Map<String, dynamic>> getServerStatus() async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/status');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      if (response.statusCode == 200) {
        final body = json.decode(response.body);
        return {
          'enabled': body['enabled'] ?? false,
          'mode': body['mode'] ?? 'blacklist',
        };
      }

      return {'enabled': false, 'mode': 'blacklist'};
    } catch (e) {
      return {'enabled': false, 'mode': 'blacklist'};
    } finally {
      client.close();
    }
  }

  /// Adds a new blocked application
  Future<bool> addBlockedApplication(String applicationName, {String mode = 'blacklist'}) async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .post(
            uri,
            headers: headers,
            body: json.encode({'name': applicationName, 'mode': mode}),
          )
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 201;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Removes a blocked application
  Future<bool> removeBlockedApplication(String applicationName, {String? mode}) async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final payload = <String, dynamic>{'name': applicationName};
      if (mode != null) {
        payload['mode'] = mode;
      }

      final response = await client
          .delete(
            uri,
            headers: headers,
            body: json.encode(payload),
          )
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Resets all blocked applications
  Future<bool> resetBlockedApplications() async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/applications/reset');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .delete(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Toggles server status (enable/disable) with optional mode
  Future<bool> toggleServerStatus(bool enabled, {String? mode}) async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/status');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final payload = <String, dynamic>{'enabled': enabled};
      if (mode != null) {
        payload['mode'] = mode;
      }

      final response = await client
          .put(uri, headers: headers, body: json.encode(payload))
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Gets client entries from the server
  Future<List<Map<String, dynamic>>> getClientData() async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/client');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      if (response.statusCode == 200) {
        final body = json.decode(response.body);
        final List<dynamic> entries = body['entries'] ?? [];
        return entries.cast<Map<String, dynamic>>();
      }

      return [];
    } catch (e) {
      return [];
    } finally {
      client.close();
    }
  }

  /// Updates an application's enabled status
  Future<bool> updateApplicationStatus(String name, bool enabled, {String? mode}) async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final body = <String, dynamic>{'name': name, 'enabled': enabled};
      if (mode != null) body['mode'] = mode;

      final response = await client
          .put(
            uri,
            headers: headers,
            body: json.encode(body),
          )
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Updates a client entry status
  Future<bool> updateClientStatus(String name, bool status) async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/client');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .put(
            uri,
            headers: headers,
            body: json.encode({'name': name, 'status': status}),
          )
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Gets computers data from the server
  Future<Map<String, dynamic>> getComputersData() async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/computers');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      if (response.statusCode == 200) {
        final body = json.decode(response.body);
        final List<dynamic> computersList = body['computers'] ?? [];
        return {
          'computers': computersList.cast<Map<String, dynamic>>(),
          'current_time': body['current_time'] != null
              ? DateTime.parse(body['current_time'] as String)
              : null,
        };
      }

      return {'computers': [], 'current_time': null};
    } catch (e) {
      return {'computers': [], 'current_time': null};
    } finally {
      client.close();
    }
  }

  /// Updates computer blocked status
  Future<bool> updateComputerBlocked(int computerId, bool blocked) async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/computers');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .put(
            uri,
            headers: headers,
            body: json.encode({'identity': computerId, 'blocked': blocked}),
          )
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Resets all computers (unblocks all)
  Future<bool> resetAllComputers() async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/computers/reset');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .delete(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }

  /// Blocks all computers
  Future<bool> blockAllComputers() async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/computers/block_all');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .put(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }
}
