import 'widgets.dart';

enum TabAnimation { slide, fade }

const defaultAccentColors = [0xff007aff, 0xffcb30e0, 0xffff2d55, 0xffff383c, 0xffff8d28, 0xffffcc00, 0xff34c759, 0xff8e8e93];

TabAnimation tabAnimationOf(Json preferences) => preferences['tabAnimation'] == 'fade' ? TabAnimation.fade : TabAnimation.slide;

ColorScheme appColorScheme(BoxController box, Brightness brightness, {Color? color, bool? pureBlack}) {
  final prefs = box.preferences;
  final accent = color ?? Color(prefs['systemColor'] == true ? box.systemAccentColor : (prefs['color'] as num?)?.toInt() ?? 0xff007aff);
  final style = ColorStyle.values.firstWhere((v) => v.name == prefs['colorStyle'], orElse: () => ColorStyle.vivid);
  return seededColorScheme(seedColor: accent, brightness: brightness, style: style).toPureBlack(pureBlack ?? prefs['pureBlack'] == true);
}

bool customFontScale(Json prefs) => prefs['customFontScale'] as bool? ?? prefs.containsKey('fontScale');

double appTextScale(Json prefs, double systemScale) =>
    (customFontScale(prefs) ? (prefs['fontScale'] as num?)?.toDouble() ?? 1 : systemScale).clamp(.8, 1.4);
