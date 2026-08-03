import 'package:flutter/material.dart';

/// The room dropdown shared by the screens whose content is room-scoped.
class RoomSelector extends StatelessWidget {
  const RoomSelector({
    super.key,
    required this.rooms,
    required this.selected,
    required this.onChanged,
  });

  final List<Map<String, dynamic>> rooms;
  final Map<String, dynamic>? selected;
  final ValueChanged<Map<String, dynamic>?> onChanged;

  @override
  Widget build(BuildContext context) {
    if (rooms.isEmpty) return const Text('Guardian');
    return DropdownButtonHideUnderline(
      child: DropdownButton<String>(
        value: selected?['id'] as String?,
        dropdownColor: const Color(0xFF2D3748),
        iconEnabledColor: Colors.white,
        style: const TextStyle(color: Colors.white, fontSize: 18),
        items: [
          for (final room in rooms)
            DropdownMenuItem(
              value: room['id'] as String,
              child: Text(room['name'] as String),
            ),
        ],
        onChanged: (id) => onChanged(
          rooms.firstWhere(
            (r) => r['id'] == id,
            orElse: () => <String, dynamic>{},
          ),
        ),
      ),
    );
  }
}

/// Resolves the room a screen should show: the stored one when it still
/// exists, otherwise the first. Returns null when the account has no rooms.
Map<String, dynamic>? pickRoom(
  List<Map<String, dynamic>> rooms,
  String? preferredId,
) {
  if (rooms.isEmpty) return null;
  for (final room in rooms) {
    if (room['id'] == preferredId) return room;
  }
  return rooms.first;
}
