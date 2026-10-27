import 'dart:async';
import 'dart:convert';
import 'package:flutter/services.dart';
import '../widgets.dart';
import '../components/form.dart';

const defaultBackupFileName = 'B-box-backup.json';

// Layout and dialogs adapted from FlClash v0.9.2 backup_and_restore.dart.
class BackupPage extends StatefulWidget {
  const BackupPage({super.key});
  @override State<BackupPage> createState() => _BackupPageState();
}

class _BackupPageState extends State<BackupPage> {
  Json? _account;
  Object? _accountError;
  bool _loaded = false, _locked = false, _loading = false, _checking = false;
  bool? _connected;
  String? _connectionError;
  int _generation = 0, _accountRevision = 0;

  @override void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_loaded) { _loaded = true; unawaited(_loadAccount()); }
  }

  Future<void> _loadAccount() async {
    final revision = _accountRevision;
    try {
      final value = await context.box.call('webdav.get');
      if (mounted && revision == _accountRevision) _updateAccount(value == null ? null : json(value));
    } catch (error) { if (mounted && revision == _accountRevision) setState(() => _accountError = error); }
  }

  void _updateAccount(Json? value) {
    _accountRevision++;
    final previous = _account;
    setState(() { _account = value; _accountError = null; });
    if (previous == null || value == null || ['uri', 'user', 'password'].any((key) => previous[key] != value[key])) {
      unawaited(_check());
    }
  }

  Future<void> _check() async {
    final generation = ++_generation;
    if (_account == null) { setState(() { _checking = false; _connected = null; }); return; }
    setState(() { _checking = true; _connected = null; _connectionError = null; });
    try {
      final connected = await context.box.call('webdav.check') == true;
      if (mounted && generation == _generation) setState(() => _connected = connected);
    } catch (error) {
      if (mounted && generation == _generation) setState(() { _connected = false; _connectionError = BoxController.describe(error); });
    } finally { if (mounted && generation == _generation) setState(() => _checking = false); }
  }

  Future<void> _edit() async {
    if (_locked) return;
    setState(() => _locked = true);
    try {
      final result = await openForm<Json>(context, WebDAVForm(initial: _account));
      if (mounted && result != null) _updateAccount(result['deleted'] == true ? null : result);
    } finally { if (mounted) setState(() => _locked = false); }
  }

  Future<void> _fileName(String? value) async {
    if (value == null || _account == null || _locked) return;
    final box = context.box;
    setState(() => _locked = true);
    try {
      final result = await box.call('webdav.set', {'value': {..._account!, 'fileName': value}});
      if (mounted) _updateAccount(json(result));
    } catch (error) { box.report(error); }
    finally { if (mounted) setState(() => _locked = false); }
  }

  void _showLoading(bool value) { if (mounted) setState(() => _loading = value); }
  Future<void> _success(String title, String message) => showFloatingDialog<void>(context: context,
    child: CommonDialog(title: context.tr(title), child: Text(context.tr(message)), actions: [
      TextButton(onPressed: () => Navigator.pop(context), child: Text(context.tr('Close'))),
    ]));

  Future<void> _backup(bool remote) async {
    if (_locked) return;
    final box = context.box;
    setState(() { _locked = true; _loading = true; });
    try {
      bool saved;
      if (remote) {
        saved = await box.call('webdav.upload') == true;
      } else {
        final data = await box.call('backup.create');
        saved = await box.call('document.save', {'name': defaultBackupFileName, 'content': jsonEncode(data)}) == true;
      }
      _showLoading(false);
      if (saved && mounted) await _success('Backup', 'Backup complete');
    } catch (error) { box.report(error); }
    finally { if (mounted) setState(() { _loading = false; _locked = false; }); }
  }

  Future<void> _restore(bool remote) async {
    if (_locked || context.box.remote) return;
    final box = context.box;
    final strategy = box.preferences['restoreStrategy'] as String? ?? 'Compatible';
    setState(() => _locked = true);
    try {
      final scope = await showFloatingDialog<String>(context: context, child: const RestoreScopeDialog());
      if (scope == null || !mounted) return;
      _showLoading(true);
      final data = await box.call(remote ? 'webdav.read' : 'backup.read', {'scope': scope});
      _showLoading(false);
      if (data == null || !mounted) return;
      final count = rows(json(data)['profiles']).length;
      final approved = await showFloatingDialog<bool>(context: context, child: CommonDialog(
        title: context.tr('Restore backup'), destructive: strategy == 'Override',
        child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, spacing: 12, children: [
          Text(context.tr('Restore {count} profiles?').replaceAll('{count}', '$count')),
          Text(context.tr(strategy == 'Override'
            ? 'Profiles missing from this backup will be removed.'
            : 'Matching profiles will be replaced. Other profiles will be kept.')),
          if (scope == 'all') Text(context.tr('Application settings and remote servers will also be restored.')),
        ]),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(context.tr('Cancel'))),
          TextButton(onPressed: () => Navigator.pop(context, true), child: Text(context.tr('Restore'))),
        ],
      ));
      if (approved != true || !mounted) return;
      _showLoading(true);
      await box.call('backup.restore', {'backup': data, 'scope': scope, 'strategy': strategy});
      _showLoading(false);
      if (!mounted) return;
      if (scope == 'all') await _loadAccount();
      if (mounted) await _success('Restore', 'Restore complete');
    } catch (error) { box.report(error); }
    finally { if (mounted) setState(() { _loading = false; _locked = false; }); }
  }

  Widget _indicator() {
    final label = _checking ? 'Checking connection' : _connected == true ? 'Connected' : 'Connection failed';
    return Semantics(label: context.tr(label), child: Tooltip(message: _connectionError ?? context.tr(label),
      child: FadeThroughBox(child: _checking ? const CommonCircleLoading(size: 12)
        : SizedBox.square(dimension: 12, child: DecoratedBox(decoration: BoxDecoration(shape: BoxShape.circle,
          color: _connected == true ? context.colorScheme.success : context.colorScheme.error))))));
  }

  @override Widget build(BuildContext context) {
    final account = _account;
    return BoxEditGuard(dirty: false, busy: _locked, child: CommonScaffold(title: context.tr('Backup and restore'), isLoading: _loading,
      body: AbsorbPointer(absorbing: _locked, child: ListView(
        padding: EdgeInsets.fromLTRB(16, context.sectionsTopPadding, 16, 16 + context.bottomInset), children: [
          generateSection(title: context.tr('Remote'), items: [
            if (_accountError != null) ErrorState(_accountError!, retry: _loadAccount)
            else if (account == null) ListItem(leading: const GlyphIcon(AppGlyphs.account),
              title: Text(context.tr('No account')), subtitle: Text(context.tr('Bind a WebDAV account')),
              trailing: TonalButton(label: context.tr('Bind'), onPressed: _locked ? null : _edit))
            else ...[
              ListItem(leading: const GlyphIcon(AppGlyphs.account),
                title: TooltipText(text: Text((account['user'] as String).isEmpty ? account['uri'] as String : account['user'] as String,
                  maxLines: 1, overflow: TextOverflow.ellipsis)),
                subtitle: Padding(padding: const EdgeInsets.symmetric(vertical: 4), child: Row(spacing: 8, children: [
                  Flexible(child: Text(context.tr('Connectivity'), maxLines: 1, overflow: TextOverflow.ellipsis)), _indicator(),
                ])), trailing: TonalButton(label: context.tr('Edit'), onPressed: _locked ? null : _edit)),
              ListItem.input(title: Text(context.tr('File')), subtitle: Text(account['fileName'] as String),
                dialogTitle: context.tr('File'), value: account['fileName'] as String, resetValue: defaultBackupFileName,
                maxLength: 128, validator: (value) => backupFileIssue(value) == null ? null : context.tr('Invalid backup file name'), onChanged: _fileName),
              ListItem(key: const ValueKey('remote-backup'), title: Text(context.tr('Backup')), onTap: () => _backup(true)),
              ListItem(key: const ValueKey('remote-restore'), title: Text(context.tr('Restore')), onTap: context.box.remote ? null : () => _restore(true)),
            ],
          ]),
          generateSection(title: context.tr('Local'), items: [
            ListItem(key: const ValueKey('local-backup'), title: Text(context.tr('Backup')), onTap: () => _backup(false)),
            ListItem(key: const ValueKey('local-restore'), title: Text(context.tr('Restore')), onTap: context.box.remote ? null : () => _restore(false)),
          ]),
          generateSection(title: context.tr('Options'), items: [
            ListItem<String>.options(title: Text(context.tr('Restore strategy')), options: const ['Compatible', 'Override'],
              value: context.box.preferences['restoreStrategy'] as String? ?? 'Compatible', textBuilder: context.tr,
              onChanged: (value) => context.box.setPreference('restoreStrategy', value)),
          ]),
        ],
      )),
    ));
  }
}

String? backupFileIssue(String? value) => value == null || value.isEmpty || value.length > 128 ||
  value == '.' || value == '..' || value.runes.any((rune) => rune < 32 || rune == 47 || rune == 92) ? 'Invalid backup file name' : null;

class RestoreScopeDialog extends StatelessWidget {
  const RestoreScopeDialog({super.key});
  @override Widget build(BuildContext context) => CommonDialog(title: context.tr('Restore'),
    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 16), child: Wrap(children: [
      ListItem(title: Text(context.tr('Restore profiles only')), onTap: () => Navigator.pop(context, 'profiles')),
      ListItem(title: Text(context.tr('Restore all data')), onTap: () => Navigator.pop(context, 'all')),
    ]));
}

class WebDAVForm extends StatefulWidget {
  const WebDAVForm({super.key, this.initial});
  final Json? initial;
  @override State<WebDAVForm> createState() => _WebDAVFormState();
}

class _WebDAVFormState extends State<WebDAVForm> {
  late final _uri = TextEditingController(text: widget.initial?['uri'] as String? ?? '');
  late final _user = TextEditingController(text: widget.initial?['user'] as String? ?? '');
  late final _password = TextEditingController(text: widget.initial?['password'] as String? ?? '');
  bool _attempted = false, _saving = false;
  String? _error;
  bool get _dirty => _uri.text != (widget.initial?['uri'] ?? '') ||
    _user.text != (widget.initial?['user'] ?? '') || _password.text != (widget.initial?['password'] ?? '');

  @override void dispose() { _uri.dispose(); _user.dispose(); _password.dispose(); super.dispose(); }
  void _changed(String _) { setState(() => _error = null); }

  String? _issue() {
    try {
      final uri = Uri.tryParse(_uri.text.trim());
      if (uri == null || !['http', 'https'].contains(uri.scheme) || uri.host.isEmpty || uri.userInfo.isNotEmpty ||
          uri.hasQuery || uri.hasFragment || _uri.text.length > 4096) return 'Enter a WebDAV HTTP or HTTPS directory address';
      if (uri.hasPort && (uri.port <= 0 || uri.port > 65535)) return 'Enter a WebDAV HTTP or HTTPS directory address';
    } catch (_) { return 'Enter a WebDAV HTTP or HTTPS directory address'; }
    if (_user.text.length > 4096 || _password.text.length > 4096) return 'Invalid WebDAV credentials';
    return null;
  }

  Future<void> _save() async {
    if (_saving) return;
    final issue = _issue();
    if (issue != null) { setState(() => _attempted = true); return; }
    final box = context.box;
    setState(() { _saving = true; _error = null; });
    try {
      final result = await box.call('webdav.set', {'value': {'uri': _uri.text.trim(), 'user': _user.text, 'password': _password.text,
        'fileName': widget.initial?['fileName'] ?? defaultBackupFileName}});
      if (mounted) context.safeNestedPop(json(result));
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
    finally { if (mounted) setState(() => _saving = false); }
  }

  Future<void> _delete() async {
    if (_saving) return;
    if (!await confirm(context, 'Delete WebDAV account', action: 'Delete') || !mounted) return;
    setState(() { _saving = true; _error = null; });
    try {
      await context.box.call('webdav.set', {'value': null});
      if (mounted) context.safeNestedPop({'deleted': true});
    } catch (error) { if (mounted) setState(() => _error = BoxController.describe(error)); }
    finally { if (mounted) setState(() => _saving = false); }
  }

  @override Widget build(BuildContext context) {
    final issue = _attempted ? _issue() : null;
    return BoxEditGuard(dirty: _dirty, busy: _saving, child: FormPage(title: 'WebDAV configuration', onSave: _save,
      saving: _saving, error: _error ?? (issue == null ? null : context.tr(issue)), children: [
        generateSection(footer: context.tr('Enter the WebDAV directory URL. Backups are stored in its B-box folder.'), items: [
          FormRow(title: context.tr('Address'), invalid: issue == 'Enter a WebDAV HTTP or HTTPS directory address',
            trailing: FormRowTextField(controller: _uri, hintText: context.tr('Required'), keyboardType: TextInputType.url,
              textInputAction: TextInputAction.next, autocorrect: false, enableSuggestions: false,
              inputFormatters: [LengthLimitingTextInputFormatter(4096)], onChanged: _changed)),
          FormRow(title: context.tr('Account'), trailing: FormRowTextField(controller: _user, hintText: context.tr('Optional'),
            textInputAction: TextInputAction.next, autocorrect: false, enableSuggestions: false,
            inputFormatters: [LengthLimitingTextInputFormatter(4096)], onChanged: _changed)),
          FormRow(title: context.tr('Password'), trailing: FormRowSecretTextField(controller: _password, hintText: context.tr('Optional'),
            textInputAction: TextInputAction.done, inputFormatters: [LengthLimitingTextInputFormatter(4096)],
            onChanged: _changed, onFieldSubmitted: (_) => _save())),
        ]),
        if (widget.initial != null) generateSection(items: [FormRow(title: context.tr('Delete'),
          titleStyle: TextStyle(color: context.colorScheme.error), onPressed: _delete)]),
      ],
    ));
  }
}
