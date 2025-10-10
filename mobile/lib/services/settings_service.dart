import 'dart:convert';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

class SettingsService {
  static const String _serverAddressKey = 'server_address';
  static const String _defaultServerAddress = 'http://localhost:8080';

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

  /// Tests connection to the server
  Future<bool> testConnection(String serverAddress) async {
    try {
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/health');
      
      final response = await client
          .get(uri)
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
  Future<List<String>> getBlockedApplications() async {
    try {
      final serverAddress = await getServerAddress();
      final client = http.Client();
      final uri = Uri.parse('$serverAddress/applications');
      
      final response = await client
          .get(uri)
          .timeout(const Duration(seconds: 10));
      
      client.close();
      
      if (response.statusCode == 200) {
        final body = json.decode(response.body);
        final List<dynamic> applications = body['applications'] ?? [];
        return applications.cast<String>();
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
      
      final response = await client
          .get(uri)
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
      final uri = Uri.parse('$serverAddress/applications');
      
      final response = await client.post(
        uri,
        headers: {'Content-Type': 'application/json'},
        body: json.encode({'name': applicationName}),
      ).timeout(const Duration(seconds: 10));
      
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
      final uri = Uri.parse('$serverAddress/applications/$applicationName');
      
      final response = await client
          .delete(uri)
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
      final uri = Uri.parse('$serverAddress/reset');
      
      final response = await client
          .delete(uri)
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
      
      final response = await client.put(
        uri,
        headers: {'Content-Type': 'application/json'},
        body: json.encode({'enabled': enabled}),
      ).timeout(const Duration(seconds: 10));
      
      client.close();
      
      return response.statusCode == 200;
    } catch (e) {
      return false;
    }
  }
}