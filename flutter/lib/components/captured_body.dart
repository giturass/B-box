import 'dart:async';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';
import 'package:flutter/services.dart';
import '../common/body_content.dart';
import '../widgets.dart';
import 'captured_body_reader.dart';

TextStyle _monospace(BuildContext context) =>
  AppTextStyle.footnote.of(context).toJetBrainsMono.copyWith(height: 1.5, color: context.colorScheme.onSurface);

class CapturedBody extends StatefulWidget {
  const CapturedBody({super.key, required this.message, required this.preview, this.loadPage,
    this.captureId, this.direction = 'response'});
  final Json message, preview;
  final String? captureId;
  final String direction;
  // Retained for single-page widget fixtures. Production readers belong to
  // this direction's State and are released when the detail is hidden.
  final CapturedBodyPageLoader? loadPage;
  @override State<CapturedBody> createState() => _CapturedBodyState();
}

/// Shows what its [CapturedBodyReader] holds, and tells it when the body is in front.
class _CapturedBodyState extends State<CapturedBody> with AutomaticKeepAliveClientMixin<CapturedBody>, WidgetsBindingObserver {
  final _scroll = ScrollController();
  final _horizontal = ScrollController();
  final _transform = TransformationController();
  late final CapturedBodyReader _reader;
  bool _wrap = true, _pageActive = false;
  int _hexColumns = 16;

  @override bool get wantKeepAlive => true;

  @override void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _reader = CapturedBodyReader(message: widget.message, preview: widget.preview, captureId: widget.captureId,
      direction: widget.direction, loadPage: widget.loadPage, onReplaced: _resetScroll)
      ..addListener(() => setState(() {}));
  }
  @override void didChangeDependencies() {
    super.didChangeDependencies();
    _reader.attach(context.box);
    _pageActive = PageActivityScope.isActiveOf(context) && TickerMode.of(context);
    _syncVisibility();
  }
  @override void didUpdateWidget(CapturedBody oldWidget) {
    super.didUpdateWidget(oldWidget);
    // The detail page's CapturePoll supplies the live direction snapshot.
    _reader.update(message: widget.message, preview: widget.preview, captureId: widget.captureId,
      direction: widget.direction, loadPage: widget.loadPage);
  }
  @override void dispose() {
    _reader.dispose();
    WidgetsBinding.instance.removeObserver(this);
    _scroll.dispose();
    _horizontal.dispose();
    _transform.dispose();
    super.dispose();
  }

  @override void didChangeAppLifecycleState(AppLifecycleState state) {
    if (mounted && _syncVisibility()) setState(() {});
  }

  bool _syncVisibility() {
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    return _reader.setActive(_pageActive && (lifecycle == null || lifecycle == AppLifecycleState.resumed));
  }

  Future<void> _copy([String? text]) async {
    if (!_reader.current) return;
    try {
      await Clipboard.setData(ClipboardData(text: text ?? _reader.pageText));
      if (_reader.current) context.box.messages.add(context.tr('Copied'));
    } catch (error) {
      if (_reader.current) context.box.report(error);
    }
  }

  void _resetScroll() {
    if (_scroll.hasClients) _scroll.jumpTo(0);
    if (_horizontal.hasClients) _horizontal.jumpTo(0);
    _transform.value = Matrix4.identity();
  }

  Future<void> _copyDisplayed() {
    final reader = _reader, bytes = reader.content.bytes;
    return _copy(!reader.usingDerived && reader.content.error != null ? reader.pageText
      : reader.view == CapturedBodyView.hex ? [
        for (var offset = 0; offset < bytes.length; offset += _hexColumns)
          capturedHexLine(bytes, offset, _hexColumns, baseOffset: reader.offset),
      ].join('\n') : reader.shownText);
  }

  List<IconButtonData> _actions() {
    final reader = _reader, view = reader.view;
    return [
      if (view == CapturedBodyView.content && reader.canFormatJson) IconButtonData(glyph: AppGlyphs.code,
        tooltip: context.tr(reader.formatted ? 'Show original' : 'Format JSON'), isSelected: reader.formatted,
        onPressed: reader.loading ? null : reader.toggleFormatted),
      if (view == CapturedBodyView.base64 ||
          view == CapturedBodyView.content && (reader.hasDerivedPage || reader.content.kind == CapturedBodyKind.text))
        IconButtonData(glyph: AppGlyphs.wrap, tooltip: context.tr('Wrap lines'), isSelected: _wrap,
          onPressed: () { setState(() => _wrap = !_wrap); _resetScroll(); }),
      if (reader.showingImage)
        IconButtonData(glyph: AppGlyphs.reset, tooltip: context.tr('Reset zoom'),
          onPressed: () => _transform.value = Matrix4.identity()),
      IconButtonData(glyph: AppGlyphs.copy, tooltip: context.tr('Copy current body page'),
        onPressed: reader.pageText.isEmpty ? null : () => _copy()),
    ];
  }

  List<PopupMenuItem> _menuItems() => [
    PopupMenuItem(glyph: AppGlyphs.copy, label: context.tr('Copy displayed body'),
      onPressed: _reader.pageText.isEmpty && !_reader.usingDerived || _reader.showingImage ? null : _copyDisplayed),
  ];

  String _caption() {
    final reader = _reader, content = reader.content, message = reader.message, view = reader.view;
    return [
      if (reader.contentType.isNotEmpty) reader.contentType,
      if (content.charset.isNotEmpty) content.charset,
      '${bytes(message['capturedBytes'] as num? ?? 0)} / ${bytes(message['bodyBytes'] as num? ?? 0)}',
      if (view == CapturedBodyView.base64) 'Base64',
      if (reader.usingDerived) context.tr('Derived view'),
      if (reader.showsFormattedPage) 'JSON',
      if (view == CapturedBodyView.content && (reader.usingDerived || content.format != CapturedBodyFormat.plain)) reader.contentLabel,
    ].join(' · ');
  }

  /// What the reader knows about the shown page that its content does not say.
  List<Widget> _notes() {
    final reader = _reader, message = reader.message, page = reader.shownPage;
    final caption = context.listCaptionStyle, subtitle = context.listSubtitleStyle;
    return [
      if (reader.usingDerived)
        Text(context.tr('The original body page is kept for copying and byte views.'), style: caption),
      if (reader.view == CapturedBodyView.content && reader.showsProtobufFields)
        Text(context.tr('Field numbers are shown without a Protobuf schema.'), style: caption),
      if (message['bodyTruncated'] == true) Text(context.tr('Captured body was truncated.'), style: subtitle),
      if (page['truncated'] == true && message['bodyTruncated'] != true)
        Text(context.tr('Body could not be fully decoded.'), style: subtitle),
      if (page['source'] == 'raw') Text(context.tr('Showing saved bytes without decoding.'), style: subtitle),
      if ((page['error'] as String? ?? '').isNotEmpty) Text(page['error'] as String, style: subtitle),
      if (reader.content.error != null) Text(context.tr('Body data could not be read.'), style: subtitle),
      if (message['bodyComplete'] != true) Text(context.tr('Body stream is incomplete'), style: subtitle),
    ];
  }

  Widget _shown(double maxHeight) {
    final reader = _reader, content = reader.content;
    if (reader.showingImage) {
      final path = reader.imagePath;
      return _BodyImage(bytes: path == null ? content.bytes : null, path: path, height: maxHeight, transform: _transform,
        onHex: () => reader.select(CapturedBodyView.hex));
    }
    if (reader.pageText.isEmpty && !reader.usingDerived) return Text(context.tr('No body captured'), style: context.listSubtitleStyle);
    if (reader.view == CapturedBodyView.hex) {
      return _BodyHex(bytes: content.bytes, offset: reader.offset, maxHeight: maxHeight, scroll: _scroll,
        onColumns: (columns) => _hexColumns = columns);
    }
    return _BodyText(text: reader.shownText, wrap: _wrap, maxHeight: maxHeight, vertical: _scroll, horizontal: _horizontal);
  }

  List<Widget> _pager() {
    final reader = _reader;
    if (!reader.hasNextPage && !reader.hasPreviousPage) return const [];
    return [
      Text('${context.tr(reader.showsDerivedPage ? 'Derived body page' : 'Body page')}: ${reader.pageNumber}', style: context.listCaptionStyle),
      Wrap(spacing: 8, children: [
        if (reader.hasPreviousPage) TextButton(
          onPressed: reader.canTurnBack ? reader.previousPage : null, child: Text(context.tr('Previous page'))),
        if (reader.hasNextPage) TextButton(
          onPressed: reader.canTurnForward ? reader.nextPage : null, child: Text(context.tr('Next page'))),
      ]),
    ];
  }

  @override Widget build(BuildContext context) {
    super.build(context);
    final box = context.watchBox(BoxAspect.connections), reader = _reader;
    if (!reader.belongsTo(box)) return const SizedBox.shrink();
    final readable = reader.content.error == null || reader.usingDerived;
    final card = Card(shape: AppShape.xl, padding: const EdgeInsets.all(16), child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, spacing: 12, children: [
        Text(_caption(), style: context.listCaptionStyle),
        if ((reader.pageText.isNotEmpty || reader.hasDerived) && readable) SegmentedControl<CapturedBodyView>(
          segments: [
            if (reader.offersContent) (CapturedBodyView.content, context.tr(reader.contentLabel)),
            (CapturedBodyView.hex, context.tr('Hex')), (CapturedBodyView.base64, 'Base64'),
          ], value: reader.view, onChanged: reader.select,
        ),
        ..._notes(),
        if (readable) _shown(MediaQuery.sizeOf(context).height * .7)
        else SelectableText(reader.pageText, key: const ValueKey('captured-body-text'), style: context.listSubtitleStyle),
        if (reader.error != null) Text(reader.error!, style: context.listSubtitleStyle),
        if (reader.opening) Text(context.tr('Loading'), style: context.listCaptionStyle),
        if (reader.canRetryView)
          TextButton(onPressed: reader.loading ? null : reader.retryView, child: Text(context.tr('Retry body view'))),
        ..._pager(),
      ]));
    return Column(key: const ValueKey('captured-body'), crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      ListHeader(title: context.tr('Body'), trailing: TonalButtonTheme(size: TonalButtonSize.section,
        child: Row(mainAxisSize: MainAxisSize.min, spacing: appBarActionSpace, children: [
          for (final action in _actions()) AppBarActionButton(data: action),
          BoxMenu(items: _menuItems()),
        ]))),
      card,
    ]);
  }
}

/// One page of text, wrapped to the card or scrolled sideways at its own width.
class _BodyText extends StatelessWidget {
  const _BodyText({required this.text, required this.wrap, required this.maxHeight, required this.vertical, required this.horizontal});
  final String text;
  final bool wrap;
  final double maxHeight;
  final ScrollController vertical, horizontal;

  @override Widget build(BuildContext context) {
    final style = _monospace(context);
    return LayoutBuilder(builder: (context, constraints) {
      var width = constraints.maxWidth;
      if (!wrap) {
        final measure = TextPainter(text: TextSpan(text: text, style: style), textDirection: TextDirection.ltr,
          textScaler: MediaQuery.textScalerOf(context))..layout();
        width = math.max(width, measure.width + 4);
        measure.dispose();
      }
      Widget child = SizedBox(width: width, child: Directionality(textDirection: TextDirection.ltr, child: SelectableText(text,
        key: const ValueKey('captured-body-text'), style: style)));
      if (!wrap) child = Scrollbar(controller: horizontal,
        child: SingleChildScrollView(controller: horizontal, primary: false, scrollDirection: Axis.horizontal, child: child));
      return ConstrainedBox(constraints: BoxConstraints(maxHeight: maxHeight), child: Scrollbar(controller: vertical,
        child: SingleChildScrollView(controller: vertical, primary: false, child: child)));
    });
  }
}

/// The bytes of one page as hexadecimal lines, with as many bytes per line as fit the card.
class _BodyHex extends StatelessWidget {
  const _BodyHex({required this.bytes, required this.offset, required this.maxHeight, required this.scroll, required this.onColumns});
  final Uint8List bytes;
  /// Where the page starts in the body; lines are numbered from it.
  final int offset;
  final double maxHeight;
  final ScrollController scroll;
  /// Receives the bytes per line in use, so that copying produces the lines on screen.
  final ValueChanged<int> onColumns;

  @override Widget build(BuildContext context) {
    final style = _monospace(context);
    return LayoutBuilder(builder: (context, constraints) {
      final measure = TextPainter(text: TextSpan(text: '0', style: style), textDirection: TextDirection.ltr,
        textScaler: MediaQuery.textScalerOf(context))..layout();
      final width = measure.width, lineHeight = measure.height + 2;
      measure.dispose();
      final columns = [16, 8, 4, 2, 1].firstWhere((count) =>
        capturedHexLineLength(bytes.length, count, baseOffset: offset) * width <= constraints.maxWidth,
        orElse: () => 1);
      onColumns(columns);
      final count = (bytes.length / columns).ceil();
      return SizedBox(height: math.min(maxHeight, count * lineHeight), child: Scrollbar(controller: scroll,
        child: ListView.builder(controller: scroll, primary: false, padding: EdgeInsets.zero, itemExtent: lineHeight, itemCount: count,
          itemBuilder: (context, index) => Directionality(textDirection: TextDirection.ltr, child: SelectableText(
            capturedHexLine(bytes, index * columns, columns, baseOffset: offset),
            key: ValueKey('captured-body-hex-line-${offset + index * columns}'),
            style: style, minLines: 1, maxLines: 1,
          ))),
      ));
    });
  }
}

class _BodyImage extends StatefulWidget {
  const _BodyImage({this.bytes, this.path, required this.height, required this.onHex, required this.transform})
    : assert((bytes == null) != (path == null));
  final Uint8List? bytes;
  final String? path;
  final double height;
  final TransformationController transform;
  final VoidCallback onHex;
  @override State<_BodyImage> createState() => _BodyImageState();
}

class _BodyImageState extends State<_BodyImage> {
  ResizeImage? _provider;
  int _width = 0, _height = 0;
  void _evict() { if (_provider != null) unawaited(_provider!.evict()); }
  @override void didUpdateWidget(_BodyImage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.bytes, widget.bytes) || oldWidget.path != widget.path) { _evict(); _provider = null; }
  }
  @override void dispose() { _evict(); super.dispose(); }
  @override Widget build(BuildContext context) => LayoutBuilder(builder: (context, constraints) {
    final ratio = MediaQuery.devicePixelRatioOf(context);
    final width = (constraints.maxWidth * ratio).ceil().clamp(1, 2048).toInt();
    final height = (widget.height * ratio).ceil().clamp(1, 2048).toInt();
    if (_provider == null || width != _width || height != _height) {
      _evict();
      _width = width; _height = height;
      final ImageProvider<Object> provider = widget.path != null ? FileImage(File(widget.path!)) : MemoryImage(widget.bytes!);
      _provider = ResizeImage(provider, width: width, height: height, policy: ResizeImagePolicy.fit);
    }
    final image = Image(key: const ValueKey('captured-body-image'), image: _provider!, fit: BoxFit.contain,
      width: constraints.maxWidth, height: widget.height, semanticLabel: context.tr('Captured image'),
      errorBuilder: (context, error, stack) => Center(child: Column(mainAxisSize: MainAxisSize.min, children: [
        Text(context.tr('Image preview unavailable'), style: context.listSubtitleStyle),
        TextButton(onPressed: widget.onHex, child: Text(context.tr('Show hexadecimal'))),
      ])));
    return ClipRect(child: InteractiveViewer(transformationController: widget.transform, minScale: 1, maxScale: 6, child: image));
  });
}
