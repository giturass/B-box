import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/form.dart';

const _requiredFunctions = ['SSL_new', 'SSL_free', 'SSL_read', 'SSL_write'];
const _optionalFunctions = ['SSL_clear', 'SSL_read_ex', 'SSL_write_ex',
  'SSL_read_early_data', 'SSL_write_early_data', 'SSL_connect', 'SSL_do_handshake'];

List<Json> copyTLSOffsets(dynamic value) => rows(value).map((entry) => <String, dynamic>{
  ...entry, 'functions': {...json(entry['functions'])},
}).toList();

class TLSOffsetsPage extends StatefulWidget {
  const TLSOffsetsPage({super.key, required this.initial});
  final List<Json> initial;
  @override State<TLSOffsetsPage> createState() => _TLSOffsetsPageState();
}

class _TLSOffsetsPageState extends State<TLSOffsetsPage> {
  late final _entries = copyTLSOffsets(widget.initial);
  bool _dirty = false;

  Future<void> _edit([int? index]) async {
    final value = await openPage<Json>(context, TLSOffsetEditorPage(
      initial: index == null ? const {} : _entries[index],
      others: [for (var i = 0; i < _entries.length; i++) if (i != index) _entries[i]],
    ));
    if (value == null || !mounted) return;
    setState(() {
      if (index == null) { _entries.add(value); } else { _entries[index] = value; }
      _dirty = true;
    });
  }

  void _done() {
    setState(() => _dirty = false);
    Navigator.pop(context, copyTLSOffsets(_entries));
  }

  @override Widget build(BuildContext context) => BoxEditGuard(dirty: _dirty,
    child: FormPage(title: 'Manual TLS function offsets', onSave: _done, children: [
      ListFooter(text: context.tr('Changes take effect after saving capture settings. Each library needs its own offsets and ELF SHA-256.')),
      if (_entries.isEmpty) EmptyState(context.tr('No manual TLS offsets')),
      if (_entries.isNotEmpty) generateSection(items: [
        for (var i = 0; i < _entries.length; i++) ListItem(
          key: ValueKey('tls-offset-entry-$i'),
          title: Text((_entries[i]['entry']?.toString().isNotEmpty ?? false)
            ? _entries[i]['entry'].toString() : _entries[i]['path'].toString().split('/').last),
          subtitle: Text(_entries[i]['path'].toString(), softWrap: true),
          onTap: () => _edit(i),
          trailing: IconButton(tooltip: context.tr('Delete'), icon: const GlyphIcon(AppGlyphs.delete),
            onPressed: () => setState(() { _entries.removeAt(i); _dirty = true; })),
        ),
      ]),
      if (_entries.length < 16) ListItem(key: const ValueKey('add-tls-offset'),
        leading: const GlyphIcon(AppGlyphs.add), title: Text(context.tr('Add TLS library')), onTap: _edit),
      ListFooter(text: context.tr('Up to 16 libraries. Manual offsets take priority over automatic resolution for the specified file.')),
    ]));
}

class TLSOffsetEditorPage extends StatefulWidget {
  const TLSOffsetEditorPage({super.key, this.initial = const {}, this.others = const []});
  final Json initial;
  final List<Json> others;
  @override State<TLSOffsetEditorPage> createState() => _TLSOffsetEditorPageState();
}

class _TLSOffsetEditorPageState extends State<TLSOffsetEditorPage> {
  late final _path = TextEditingController(text: widget.initial['path']?.toString() ?? '');
  late final _entry = TextEditingController(text: widget.initial['entry']?.toString() ?? '');
  late final _sha = TextEditingController(text: widget.initial['sha256']?.toString() ?? '');
  late final _functions = {for (final name in [..._requiredFunctions, ..._optionalFunctions])
    name: TextEditingController(text: json(widget.initial['functions'])[name]?.toString() ?? '')};
  final _scroll = ScrollController();
  bool _dirty = false;
  String? _error;

  @override void dispose() {
    for (final controller in [_path, _entry, _sha, ..._functions.values]) { controller.dispose(); }
    _scroll.dispose();
    super.dispose();
  }

  void _save() {
    final path = _path.text.trim(), entry = _entry.text.trim(), sha = _sha.text.trim();
    final functions = <String, String>{};
    String? error;
    if (!path.startsWith('/') || path.length > 4096 || RegExp(r'[\x00\r\n]').hasMatch(path)) {
      error = context.tr('Enter an absolute ELF library or APK path.');
    } else if (entry.isNotEmpty && (entry.length > 512 ||
        !RegExp(r'^lib/(arm64-v8a|x86_64)/[^/\\\x00\r\n]+\.so$').hasMatch(entry))) {
      error = context.tr('APK entry must be lib/arm64-v8a/name.so or lib/x86_64/name.so.');
    } else if (!RegExp(r'^[0-9a-fA-F]{64}$').hasMatch(sha)) {
      error = context.tr('Enter the 64 hexadecimal digits of the ELF SHA-256.');
    } else if (widget.others.any((other) => other['path'] == path && (other['entry'] ?? '') == entry)) {
      error = context.tr('This library already has manual offsets.');
    } else {
      final seen = <String>{};
      for (final field in _functions.entries) {
        final value = field.value.text.trim();
        if (value.isEmpty && _optionalFunctions.contains(field.key)) continue;
        if (!RegExp(r'^0[xX][0-9a-fA-F]{1,16}$').hasMatch(value) ||
            BigInt.parse(value.substring(2), radix: 16) == BigInt.zero) {
          error = '${field.key}: ${context.tr('Enter a nonzero hexadecimal ELF file offset (0x...).')}';
          break;
        }
        final offset = '0x${BigInt.parse(value.substring(2), radix: 16).toRadixString(16)}';
        if (!seen.add(offset)) {
          error = context.tr('Function offsets must be distinct.');
          break;
        }
        functions[field.key] = offset;
      }
    }
    if (error != null) {
      setState(() => _error = error);
      if (_scroll.hasClients) _scroll.animateTo(0, duration: const Duration(milliseconds: 200), curve: Curves.easeOut);
      return;
    }
    setState(() => _dirty = false);
    Navigator.pop(context, <String, dynamic>{'path': path, if (entry.isNotEmpty) 'entry': entry,
      'sha256': sha.toLowerCase(), 'functions': functions});
  }

  Widget _field(String key, TextEditingController controller, String title, {String? hint, String? footer, int limit = 64}) => generateSection(
    title: context.tr(title), footer: footer == null ? null : context.tr(footer),
    items: [DecorationListItem(title: TextField(key: ValueKey('tls-offset-$key'), controller: controller,
      minLines: 1, maxLines: key == 'path' || key == 'entry' || key == 'sha256' ? 3 : 1,
      keyboardType: TextInputType.text, textInputAction: TextInputAction.next,
      inputFormatters: [LengthLimitingTextInputFormatter(limit)], autocorrect: false, enableSuggestions: false,
      style: AppTextStyle.body.of(context),
      decoration: InputDecoration.collapsed(border: const NoInputBorder(), hintText: hint),
      onChanged: (_) => setState(() { _dirty = true; _error = null; }),
    ))],
  );

  @override Widget build(BuildContext context) => BoxEditGuard(dirty: _dirty,
    child: FormPage(title: 'TLS library offsets', onSave: _save, error: _error, controller: _scroll, children: [
      ListFooter(text: context.tr('For the public OpenSSL/BoringSSL SSL ABI. Enter ELF file offsets, not runtime addresses. APK container offsets are added automatically. Go TLS uses automatic ABI validation.')),
      _field('path', _path, 'Library or APK path', hint: '/data/app/.../lib/arm64/libssl.so', limit: 4096),
      _field('entry', _entry, 'APK library entry', hint: 'lib/arm64-v8a/libcronet.so', limit: 512,
        footer: 'Leave empty for a standalone ELF library. For an APK, enter the exact uncompressed library entry.'),
      _field('sha256', _sha, 'ELF SHA-256', limit: 128,
        footer: 'Hash the ELF library bytes, excluding the APK container. A changed library is rejected before attachment.'),
      ListFooter(text: context.tr('Required functions')),
      for (final name in _requiredFunctions) _field(name, _functions[name]!, name, hint: '0x...'),
      ListFooter(text: context.tr('Optional functions')),
      for (final name in _optionalFunctions) _field(name, _functions[name]!, name, hint: '0x...'),
    ]));
}
