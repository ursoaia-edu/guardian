import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:procsentinel_mobile/screens/login_screen.dart';
import 'package:procsentinel_mobile/services/settings_service.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    SettingsService().resetForTest();
  });

  testWidgets('the sign-in screen asks for a server, an email and a password', (
    tester,
  ) async {
    await SettingsService().load();
    await tester.pumpWidget(const MaterialApp(home: LoginScreen()));

    expect(find.text('Server address'), findsOneWidget);
    expect(find.text('Email'), findsOneWidget);
    expect(find.text('Password'), findsOneWidget);
    // The shared admin token this screen replaced has no field any more.
    expect(find.textContaining('Token'), findsNothing);
    expect(find.text('Sign in'), findsOneWidget);
  });

  testWidgets('registering asks for a name as well', (tester) async {
    await SettingsService().load();
    await tester.pumpWidget(const MaterialApp(home: LoginScreen()));

    await tester.tap(find.text('Create a new account'));
    await tester.pump();

    expect(find.text('Your name (optional)'), findsOneWidget);
    expect(find.text('Create account'), findsOneWidget);
  });
}
