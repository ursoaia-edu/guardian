import 'package:flutter/material.dart';

void showTopSnackBar(
  BuildContext context, {
  required String message,
  Color? backgroundColor,
  Duration duration = const Duration(seconds: 1),
}) {
  ScaffoldMessenger.of(context).showSnackBar(
    SnackBar(
      content: Text(message),
      backgroundColor: backgroundColor,
      duration: duration,
      behavior: SnackBarBehavior.floating,
      margin: EdgeInsets.only(
        top: 50,
        bottom: MediaQuery.of(context).size.height - 150,
        left: 10,
        right: 10,
      ),
    ),
  );
}
