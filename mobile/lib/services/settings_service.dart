import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

class SettingsService {
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
    try {
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/health');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      client.close();

      if (response.statusCode == 200) {
        // Try to parse the health check response
        try {
          final body = json.decode(response.body);
          return body['status'] == 'ok';
        } catch (_) {
          // If JSON parsing fails, just check status code
          return true;
        }
      }

      return false;
    } catch (e) {
      return false;
    }
  }

  /// Gets the list of blocked applications from the server
  Future<List<Map<String, dynamic>>> getBlockedApplications() async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      client.close();

      if (response.statusCode == 200) {
        final body = json.decode(response.body);
        final List<dynamic> applications = body['applications'] ?? [];
        return applications.cast<Map<String, dynamic>>();
      }

      return [];
    } catch (e) {
      return [];
    }
  }

  /// Gets the server status
  Future<bool> getServerStatus() async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/status');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      client.close();

      if (response.statusCode == 200) {
        final body = json.decode(response.body);
        return body['enabled'] ?? false;
      }

      return false;
    } catch (e) {
      return false;
    }
  }

  /// Adds a new blocked application
  Future<bool> addBlockedApplication(String applicationName) async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .post(
            uri,
            headers: headers,
            body: json.encode({'name': applicationName}),
          )
          .timeout(const Duration(seconds: 10));

      client.close();

      return response.statusCode == 201;
    } catch (e) {
      return false;
    }
  }

  /// Removes a blocked application
  Future<bool> removeBlockedApplication(String applicationName) async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .delete(
            uri,
            headers: headers,
            body: json.encode({'name': applicationName}),
          )
          .timeout(const Duration(seconds: 10));

      client.close();

      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }

  /// Resets all blocked applications
  Future<bool> resetBlockedApplications() async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/manage/applications/reset');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .delete(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      client.close();

      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }

  /// Toggles server status (enable/disable)
  Future<bool> toggleServerStatus(bool enabled) async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/status');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .put(uri, headers: headers, body: json.encode({'enabled': enabled}))
          .timeout(const Duration(seconds: 10));

      client.close();

      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }

  /// Gets system data from the server
  Future<List<Map<String, dynamic>>> getSystemData() async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/system');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      client.close();

      if (response.statusCode == 200) {
        final body = json.decode(response.body);
        final List<dynamic> systems = body['systems'] ?? [];
        return systems.cast<Map<String, dynamic>>();
      }

      return [];
    } catch (e) {
      return [];
    }
  }

  /// Updates an application's enabled status
  Future<bool> updateApplicationStatus(String name, bool enabled) async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .put(
            uri,
            headers: headers,
            body: json.encode({'name': name, 'enabled': enabled}),
          )
          .timeout(const Duration(seconds: 10));

      client.close();

      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }

  /// Updates system status
  Future<bool> updateSystemStatus(String name, bool status) async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/system');
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

      client.close();

      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }

  /// Gets computers data from the server
  Future<Map<String, dynamic>> getComputersData() async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/manage/computers');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      client.close();

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
    }
  }

  /// Updates computer blocked status
  Future<bool> updateComputerBlocked(int computerId, bool blocked) async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
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

      client.close();

      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }

  /// Resets all computers (unblocks all)
  Future<bool> resetAllComputers() async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/manage/computers/reset');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .delete(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      client.close();

      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }

  /// Blocks all computers
  Future<bool> blockAllComputers() async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/manage/computers/block_all');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .put(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      client.close();

      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }
}
