# prism_ui

**A distinctive iOS-style UI library for Flutter, in every color you can pick.**

Start with one color. Choose a style. Every component follows, in light and dark.

## Highlights

- **Feels like iOS.** Liquid glass, fluid transitions, sheets that snap into place, and soft continuous corners.
- **Yours in any color.** Four theme styles turn a single color into a complete look.
- **Light and dark, equally polished.** Both modes are tuned to match, so neither feels like an afterthought.
- **Nothing extra.** Built on Flutter's widgets alone, with no Material or Cupertino underneath.

## Installation

```yaml
dependencies:
  prism_ui: ^0.1.2
```

## Quick start

```dart
import 'package:flutter/widgets.dart';
import 'package:prism_ui/prism_ui.dart';

void main() => runApp(
  PrismApp(
    theme: ThemeData(
      colorScheme: ColorScheme.fromSeed(
        seedColor: const Color(0xFF007AFF),
        style: ColorStyle.vivid,
      ),
    ),
    localizationsDelegates: const [MyUiLocalizations.delegate],
    builder: (context, child) {
      final size = MediaQuery.sizeOf(context);
      return ViewScope(size: size, isMobile: size.width < 600, child: child!);
    },
    home: const HomePage(),
  ),
);

class HomePage extends StatelessWidget {
  const HomePage({super.key});

  @override
  Widget build(BuildContext context) => Surface(
    color: context.colorScheme.surface,
    child: Center(
      child: ListRow(
        title: const Text('Liquid glass'),
        trailing: Switch(value: true, onChanged: (_) {}),
      ),
    ),
  );
}
```

`MyUiLocalizations` is your own implementation of `UiLocalizations`, which holds the few strings the components
display themselves.

## Color styles

| Style | Character |
|---|---|
| `vivid` | Bold. Your color takes center stage as the accent. |
| `tinted` | Immersive. Backgrounds take on the hue of your color too. |
| `soft` | Calm. A gentler, more muted take on your color. |
| `monochrome` | Pure. All the hue is gone, leaving only tone. |

## Components

| | |
|---|---|
| **Structure** | `PrismApp`, `PrismScaffold`, `AppBar`, `NavigationDock`, `NavigationSidebar` |
| **Overlays** | Sheets, form sheets, side sheets, dialogs, popup menus, context menus, banners |
| **Controls** | Buttons, `Switch`, `Checkbox`, `Slider`, `SegmentedControl`, `Chip`, text fields, search |
| **Lists** | `ListRow`, grouped lists, drag to reorder, high-performance slivers |
| **Effects** | `LiquidGlass`, press feedback, page transitions, skeletons, loading indicators |
| **Foundation** | Typography, surfaces, superellipse shapes, and a stroke-based icon engine |

## Make it yours

prism_ui ships without assets or native code, so the details that make your app feel like yours stay in your hands:

- **Icons:** draw your own `Glyph`s on the same engine.
- **Emoji:** bring your own font through `PrismApp.emojiFont`.
- **Empty states:** give `NullStatus` any `Illustration`.

## Using it alongside Material

A few names match Material's, such as `Switch`, `AppBar` and `ThemeData`. In a file that imports both, use `hide` or
a prefix.

## License

[GPL-3.0](LICENSE). Third-party code adapted here keeps its original license, see [NOTICE](NOTICE).
