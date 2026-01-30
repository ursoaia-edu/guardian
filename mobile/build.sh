#!/usr/bin/env bash
mkdir -p ../dist
flutter clean

flutter build apk --release
cp build/app/outputs/flutter-apk/app-release.apk ../dist/guardian.apk

# flutter build web --release
# cp -r build/web ../bin/web

# flutter build windows --release
# cp build/windows/runner/Release/procsentinel.exe ../release/guardian.exe

# flutter build linux --release
# cp build/linux/x64/release/bundle/procsentinel ../release/guardian

