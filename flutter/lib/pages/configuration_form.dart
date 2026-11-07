import 'dart:convert';
import '../widgets.dart';
import '../common/config_form.dart';
import '../components/form.dart';
import 'configuration.dart' show ConfigValueEditor;
import 'request_settings.dart' show CaptureAppsPage;

String _formError(BuildContext context, Object error) => context.tr(error is FormatException
  ? error.message : error is StateError ? error.message.toString() : BoxController.describe(error));

class ConfigEntryForm extends StatefulWidget {
  const ConfigEntryForm({super.key, required this.kind, required this.initial, required this.onSave,
    this.isNew = false, this.outbounds = const [], this.servers = const [], this.ruleSets = const [], this.usedTags = const [], this.allowedTypes});
  final ConfigKind kind;
  final Json initial;
  final Future<bool> Function(Json) onSave;
  final bool isNew;
  final List<String> outbounds, servers, ruleSets, usedTags;
  final List<String>? allowedTypes;
  @override State<ConfigEntryForm> createState() => _ConfigEntryFormState();
}

class _ConfigEntryFormState extends State<ConfigEntryForm> {
  late Json _draft = copyConfig(widget.initial);
  final _controllers = <String, TextEditingController>{};
  final _fieldSpecs = <String, ConfigFieldSpec>{};
  final _changed = <String>{};
  final _variantDrafts = <String, Json>{};
  ScrollController? _scroll;
  bool _saving = false, _dirty = false;
  String? _error;
  List<ConfigFieldSpec> get _fields => configFields(widget.kind, _draft,
    outbounds: widget.outbounds.where((tag) => tag != _draft['tag']).toList(), servers: widget.servers, ruleSets: widget.ruleSets);

  @override void didChangeDependencies() { super.didChangeDependencies(); _scroll ??= sheetScrollController(context); }
  @override void dispose() { _scroll?.dispose(); for (final controller in _controllers.values) { controller.dispose(); } super.dispose(); }

  void _showError(Object error) {
    if (!mounted) return;
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() => _error = _formError(context, error));
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final scroll = _scroll;
      if (mounted && scroll != null && scroll.hasClients) {
        scroll.animateTo(scroll.position.minScrollExtent, duration: const Duration(milliseconds: 200), curve: Curves.easeOut);
      }
    });
  }

  TextEditingController _controller(ConfigFieldSpec field) => _controllers.putIfAbsent(field.key, () {
    _fieldSpecs[field.key] = field;
    final value = configValue(_draft, field.key) ?? field.fallback;
    return TextEditingController(text: value is List ? value.join('\n') : value?.toString() ?? '');
  });

  dynamic _parse(ConfigFieldSpec field, String text, {required bool validate}) {
    final raw = field.input == ConfigInput.secret || field.multiline ? text : text.trim();
    if (raw.isEmpty) {
      if (validate && field.required) throw FormatException('${context.tr(field.label)}: ${context.tr('Required')}');
      return null;
    }
    if (field.input == ConfigInput.integer) {
      final value = int.tryParse(raw);
      if (value == null || value < field.min || value > field.max) {
        if (validate) throw FormatException('${context.tr(field.label)}: ${field.min}–${field.max}');
        return raw;
      }
      return value;
    }
    if (field.input == ConfigInput.ports) {
      final values = raw.split(RegExp(r'[\s,;]+')).where((part) => part.isNotEmpty).map(int.tryParse).toList();
      if (values.any((port) => port == null || port < field.min || port > field.max)) {
        if (validate) throw FormatException('${context.tr(field.label)}: ${field.min}–${field.max}');
        return raw;
      }
      return values.cast<int>().toSet().toList();
    }
    if (field.input == ConfigInput.lines) return raw.split(RegExp(r'[\r\n,]+')).map((item) => item.trim()).where((item) => item.isNotEmpty).toSet().toList();
    return raw;
  }

  Json _collect({bool validate = true}) {
    final result = copyConfig(_draft);
    for (final key in _changed) {
      final field = _fieldSpecs[key]!;
      setConfigValue(result, key, _parse(field, _controllers[key]!.text, validate: validate));
    }
    for (final field in _fields) {
      final value = configValue(result, field.key) ?? field.fallback;
      if (validate && field.required && (widget.isNew || _changed.contains(field.key)) &&
          (value == null || value == '' || value is List && value.isEmpty)) {
        throw FormatException('${context.tr(field.label)}: ${context.tr('Required')}');
      }
      // Validate edited numbers and required values even before their field has focus.
      if (validate && field.input == ConfigInput.integer && (widget.isNew || _changed.contains(field.key)) && value != null &&
          (value is! int || value < field.min || value > field.max)) {
        throw FormatException('${context.tr(field.label)}: ${field.min}–${field.max}');
      }
    }
    if (validate) {
      final oldTags = configStrings(widget.initial['tag']);
      if (configStrings(result['tag']).any((tag) => widget.usedTags.contains(tag) && (widget.isNew || !oldTags.contains(tag)))) {
        throw FormatException(context.tr('This name is already in use'));
      }
      if (widget.isNew && [ConfigKind.routeRule, ConfigKind.dnsRule, ConfigKind.headlessRule].contains(widget.kind) &&
          ['', 'route', 'reject'].contains(result['action'] ?? '') &&
          !result.keys.any((key) => !['action', 'outbound', 'server', 'invert', 'type'].contains(key))) {
        throw FormatException(context.tr('Add at least one match condition'));
      }
    }
    return result;
  }

  void _replace(Json value) {
    for (final controller in _controllers.values) { controller.dispose(); }
    _controllers.clear();
    _fieldSpecs.clear();
    _changed.clear();
    _variantDrafts.clear();
    _draft = copyConfig(value);
    _dirty = true;
  }

  void _choose(ConfigFieldSpec field, dynamic value) => setState(() {
    if (['transport.type', 'obfs.type'].contains(field.key)) {
      final parent = field.key.split('.').first;
      final current = json(_collect(validate: false)[parent]);
      _variantDrafts['$parent:${current['type'] ?? ''}'] = current;
      for (final key in _controllers.keys.where((key) => key.startsWith('$parent.')).toList()) {
        _controllers.remove(key)?.dispose();
        _fieldSpecs.remove(key);
        _changed.remove(key);
      }
      if (value == '') _draft.remove(parent);
      else _draft[parent] = copyConfig(_variantDrafts['$parent:$value'] ?? {'type': value});
      _dirty = true;
      return;
    }
    if (field.key == 'action' && [ConfigKind.routeRule, ConfigKind.dnsRule].contains(widget.kind)) {
      final destination = widget.kind == ConfigKind.dnsRule ? 'server' : 'outbound';
      if (value != 'route') _draft.remove(destination);
      if (value != 'reject') { _draft.remove('method'); _draft.remove('no_drop'); }
    }
    if (field.key == 'outbounds' && value is List && !value.contains(_draft['default'])) _draft.remove('default');
    setConfigValue(_draft, field.key, value == '' || value is List && value.isEmpty ? null : value);
    _dirty = true;
  });

  Future<void> _save() async {
    if (_saving || context.box.remote) return;
    Json value;
    try { value = _collect(); } catch (error) { _showError(error); return; }
    setState(() { _saving = true; _error = null; });
    try {
      final saved = await widget.onSave(value);
      if (mounted && saved) {
        setState(() { _dirty = false; _saving = false; });
        context.safeNestedPop();
      }
    } catch (error) { _showError(error); }
    finally { if (mounted) setState(() => _saving = false); }
  }

  Widget _field(ConfigFieldSpec field) {
    final value = configValue(_draft, field.key) ?? field.fallback;
    final key = ValueKey('config-field-${field.key}');
    if (field.input == ConfigInput.toggle) return ListItem.toggle(key: key, title: Text(context.tr(field.label)),
      value: field.key == 'disabled' ? value != true : value == true,
      onChanged: (next) => _choose(field, field.key == 'disabled' ? !next : next));
    if (field.input == ConfigInput.choice) {
      final current = value?.toString() ?? '';
      return ListItem<String>.options(key: key, title: Text(context.tr(field.label)),
        options: {...field.options, current}.toList(), value: current,
        textBuilder: (option) => option.isEmpty && field.required ? context.tr('Not selected')
          : configOptionLabel(context, field.key, option), onChanged: (next) => _choose(field, next));
    }
    if ([ConfigInput.choices, ConfigInput.apps].contains(field.input)) {
      final selected = configStrings(value);
      return ListItem(key: key, title: Text(context.tr(field.label)),
        subtitle: Text(selected.isEmpty ? context.tr('Not selected') : selected.join(', '), maxLines: 2, overflow: TextOverflow.ellipsis),
        trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: () async {
          final next = await openPage<List<String>>(context, field.input == ConfigInput.apps
            ? CaptureAppsPage(selected: selected, title: 'Applications', description: 'Select applications for this condition. An empty selection adds no application filter.')
            : ConfigSelectionPage(title: field.label, selected: selected, options: field.options));
          if (next != null && mounted) _choose(field, next);
        });
    }
    final multiple = field.multiline || [ConfigInput.lines, ConfigInput.ports].contains(field.input);
    return Padding(padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10), child: BoxField(
      label: '${context.tr(field.label)}${field.required ? ' *' : ''}', borderRadius: AppRadius.md,
      child: TextField(key: key, controller: _controller(field), readOnly: _saving,
        obscureText: field.input == ConfigInput.secret, minLines: multiple ? (field.multiline ? 4 : 2) : 1, maxLines: multiple ? 8 : 1,
        keyboardType: field.input == ConfigInput.integer || field.input == ConfigInput.ports ? TextInputType.number
          : multiple ? TextInputType.multiline : TextInputType.text,
        autocorrect: false, enableSuggestions: false, smartDashesType: SmartDashesType.disabled, smartQuotesType: SmartQuotesType.disabled,
        decoration: InputDecoration(hintText: field.hint ?? (field.input == ConfigInput.lines ? context.tr('One item per line') : null)),
        onChanged: (_) => setState(() { _changed.add(field.key); _dirty = true; }),
      ),
    ));
  }

  @override Widget build(BuildContext context) {
    if (context.box.remote) return const BoxPage(title: 'Profile settings', children: [EmptyState('Disconnect remote session to edit local profiles')]);
    final types = widget.allowedTypes ?? configTypes(widget.kind), type = _draft['type']?.toString() ?? '';
    return BoxEditGuard(dirty: _dirty, busy: _saving, child: FormPage(
      title: widget.isNew ? 'Add' : 'Edit', saving: _saving, error: _error, onSave: _save, controller: _scroll, children: [
        if (types.isNotEmpty) generateSection(items: [
          if (widget.isNew) ListItem<String>.options(key: const ValueKey('config-type'), title: Text(context.tr('Type')),
            options: {...types, if (type.isNotEmpty) type}.toList(), value: type,
            textBuilder: (value) => context.tr(configTypeLabel(value)), onChanged: (value) {
              if (value == type) return;
              // Only a new item may change protocol; imported protocol options stay intact.
              final tag = _collect(validate: false)['tag'];
              setState(() => _replace({...newConfigEntry(widget.kind, value), if (tag != null) 'tag': tag}));
            })
          else ListItem(title: Text(context.tr('Type')), trailing: Text(context.tr(configTypeLabel(type)))),
        ]),
        if (type == 'logical') ListFooter(text: context.tr('This rule combines several conditions. Edit the combined conditions in Advanced settings.')),
        generateSection(items: _fields.map(_field).toList()),
        if (widget.kind == ConfigKind.ruleSet && type == 'inline') _items('rules', 'Rules', ConfigKind.headlessRule),
        if (widget.kind == ConfigKind.mapLocal) _items('headers', 'Response headers', ConfigKind.header),
        if (widget.kind == ConfigKind.inbound && ['mixed', 'socks', 'http'].contains(type)) _items('users', 'Authentication', ConfigKind.user),
        generateSection(items: [ListItem(title: Text(context.tr('Advanced settings')), leading: const GlyphIcon(AppGlyphs.code),
          trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: () => openPage(context, ConfigEntryAdvancedPage(
            value: _collect(validate: false), onSave: (value) async { if (mounted) setState(() => _replace(value)); return true; })))],
          footer: context.tr('More options are available in Advanced settings.')),
      ],
    ));
  }

  Widget _items(String key, String title, ConfigKind kind) => generateSection(items: [
    ListItem(title: Text(context.tr(title)), subtitle: Text('${rows(_draft[key]).length}'),
      trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: () => openPage(context, ConfigDraftItemsPage(
        title: title, kind: kind, items: rows(_draft[key]), onChanged: (items) {
          if (mounted) setState(() { _draft[key] = items; _dirty = true; });
        }))),
  ]);
}

String configOptionLabel(BuildContext context, String key, String value) {
  if (value.isEmpty) return context.tr('Default');
  const labels = {
    'action': {'route': 'Send through', 'reject': 'Block', 'hijack-dns': 'Handle DNS', 'sniff': 'Detect protocol',
      'header': 'Rewrite request URL', '302': 'Temporary redirect (302)', '307': 'Temporary redirect (307)',
      'reject-200': 'Empty successful response', 'reject-dict': 'Empty object response'},
    'phase': {'request': 'Request', 'response': 'Response'},
    'operation': {'add': 'Add', 'delete': 'Delete', 'replace': 'Replace', 'replace_regex': 'Replace matching text'},
    'data_type': {'text': 'Text', 'file': 'File or URL', 'base64': 'Base64'},
    'format': {'binary': 'Compiled rules', 'source': 'Source rules'},
    'transport.type': {'ws': 'WebSocket', 'http': 'HTTP', 'grpc': 'gRPC', 'httpupgrade': 'HTTP Upgrade', 'quic': 'QUIC'},
  };
  return context.tr(labels[key]?[value] ?? value);
}

class ConfigEntryAdvancedPage extends StatefulWidget {
  const ConfigEntryAdvancedPage({super.key, required this.value, required this.onSave});
  final Json value;
  final Future<bool> Function(Json) onSave;
  @override State<ConfigEntryAdvancedPage> createState() => _ConfigEntryAdvancedPageState();
}
class _ConfigEntryAdvancedPageState extends State<ConfigEntryAdvancedPage> {
  late Json _value = copyConfig(widget.value);
  @override Widget build(BuildContext context) => context.box.remote
    ? const BoxPage(title: 'Advanced settings', children: [EmptyState('Disconnect remote session to edit local profiles')])
    : BoxPage(title: 'Advanced settings', children: [
    ListFooter(text: context.tr('Edit all fields as JSON. Changes are applied when you save the form.')),
    generateSection(items: [ListItem.open(leading: const GlyphIcon(AppGlyphs.code), title: Text(context.tr('Edit JSON')),
      widget: ConfigValueEditor(title: 'Edit JSON', initial: _value, onSave: (value) async {
        if (value is! Map) throw const FormatException('JSON object required');
        final saved = await widget.onSave(json(value));
        if (mounted && saved) setState(() => _value = copyConfig(json(value)));
        return saved;
      }))]),
  ]);
}

class ConfigSelectionPage extends StatefulWidget {
  const ConfigSelectionPage({super.key, required this.title, required this.selected, required this.options});
  final String title;
  final List<String> selected, options;
  @override State<ConfigSelectionPage> createState() => _ConfigSelectionPageState();
}
class _ConfigSelectionPageState extends State<ConfigSelectionPage> {
  late final _selected = widget.selected.toSet();
  @override Widget build(BuildContext context) => BoxPage(title: widget.title,
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Done'),
      onPressed: () => context.safeNestedPop(_selected.toList())),
    children: [
      if (widget.options.isEmpty && _selected.isEmpty) const EmptyState('No choices available'),
      generateSection(items: [for (final value in {...widget.options, ...widget.selected})
        ListItem.checkbox(title: Text(value), value: _selected.contains(value),
          onChanged: (_) => setState(() { if (!_selected.add(value)) _selected.remove(value); }))]),
    ]);
}

class ConfigDraftItemsPage extends StatefulWidget {
  const ConfigDraftItemsPage({super.key, required this.items, required this.title, required this.kind, required this.onChanged});
  final List<Json> items;
  final String title;
  final ConfigKind kind;
  final ValueChanged<List<Json>> onChanged;
  @override State<ConfigDraftItemsPage> createState() => _ConfigDraftItemsPageState();
}
class _ConfigDraftItemsPageState extends State<ConfigDraftItemsPage> {
  late final _rules = widget.items.map(copyConfig).toList();
  void _edit([int? index]) => openForm(context, ConfigEntryForm(kind: widget.kind, isNew: index == null,
    initial: index == null ? {} : _rules[index], onSave: (value) async {
      setState(() { if (index == null) _rules.add(value); else _rules[index] = value; });
      widget.onChanged(_rules.map(copyConfig).toList());
      return true;
    }));
  @override Widget build(BuildContext context) => BoxPage(title: widget.title,
    primaryAction: IconButtonData(glyph: AppGlyphs.add, tooltip: context.tr('Add'), onPressed: _edit), children: [
      for (var i = 0; i < _rules.length; i++) ListItem(title: Text((_rules[i]['name'] ?? _rules[i]['username'] ?? '${i + 1}').toString()),
        subtitle: Text(configEntrySummary(_rules[i], context.tr)), onTap: () => _edit(i),
        trailing: IconButton(tooltip: context.tr('Delete'), icon: const GlyphIcon(AppGlyphs.delete), onPressed: () {
          setState(() => _rules.removeAt(i));
          widget.onChanged(_rules.map(copyConfig).toList());
        })),
      if (_rules.isEmpty) const EmptyState('No items yet'),
    ]);
}

class NewProfilePage extends StatefulWidget {
  const NewProfilePage({super.key});
  @override State<NewProfilePage> createState() => _NewProfilePageState();
}
class _NewProfilePageState extends State<NewProfilePage> {
  final _name = TextEditingController();
  Json? _node;
  bool _saving = false;
  String? _error;
  @override void dispose() { _name.dispose(); super.dispose(); }
  Future<void> _save() async {
    if (_saving || context.box.remote) return;
    if (_name.text.trim().isEmpty || _node == null) { setState(() => _error = context.tr('Enter a name and configure a node')); return; }
    setState(() { _saving = true; _error = null; });
    try {
      await context.box.call('profiles.save', {'name': _name.text.trim(), 'url': '', 'autoUpdate': false, 'interval': 60,
        'content': jsonEncode(createGuidedProfile(_node!))});
      if (mounted) { _name.clear(); setState(() { _node = null; _saving = false; }); context.safeNestedPop(); }
    } catch (error) { if (mounted) setState(() => _error = _formError(context, error)); }
    finally { if (mounted) setState(() => _saving = false); }
  }
  @override Widget build(BuildContext context) => context.box.remote
    ? const BoxPage(title: 'New profile', children: [EmptyState('Disconnect remote session to edit local profiles')])
    : BoxEditGuard(dirty: _name.text.isNotEmpty || _node != null, busy: _saving,
    child: FormPage(title: 'New profile', saving: _saving, error: _error, onSave: _save, children: [
      ListFooter(text: context.tr('Enter the node details from your service provider. VPN and DNS settings are prepared automatically.')),
      generateSection(items: [
        FormRow(title: context.tr('Name'), trailing: FormRowTextField(controller: _name,
          hintText: context.tr('Required'), onChanged: (_) => setState(() {}))),
        ListItem(key: const ValueKey('new-profile-node'), title: Text(context.tr('Proxy node')),
          subtitle: Text(_node == null ? context.tr('Configure node') : configEntrySummary(_node!, context.tr)),
          trailing: const GlyphIcon(AppGlyphs.chevronForward), onTap: () => openForm(context, ConfigEntryForm(
            kind: ConfigKind.outbound, initial: _node ?? {...newConfigEntry(ConfigKind.outbound), 'tag': 'proxy'}, isNew: true,
            allowedTypes: configTypes(ConfigKind.outbound).where((type) => !['selector', 'urltest'].contains(type)).toList(),
            servers: const ['device-dns'], onSave: (value) async { setState(() => _node = value); return true; }))),
      ]),
    ]));
}
