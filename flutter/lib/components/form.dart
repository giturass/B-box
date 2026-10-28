import 'package:flutter/services.dart';
import '../widgets.dart';

// Grouped form rows adapted from FlClash v0.9.2 features/form.

class FormPage extends StatelessWidget {
  const FormPage({super.key, required this.title, required this.onSave, required this.children,
    this.error, this.saving = false, this.actions = const []});
  final String title;
  final VoidCallback onSave;
  final String? error;
  final bool saving;
  final List<Widget> children;
  final List<IconButtonData> actions;

  @override Widget build(BuildContext context) => CommonScaffold(
    title: context.tr(title), iconActions: actions,
    confirmationAction: IconButtonData(glyph: AppGlyphs.check, tooltip: context.tr('Save'),
      isLoading: saving, onPressed: saving ? null : onSave),
    body: AbsorbPointer(absorbing: saving, child: ListView(shrinkWrap: true,
      padding: EdgeInsets.fromLTRB(16, context.sectionsTopPadding, 16, 20 + context.bottomInset),
      children: [
        // Keep form children at stable indices when the validation message changes.
        if (error != null) Padding(padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
          child: Text(error!, style: context.listCaptionStyle.copyWith(color: context.colorScheme.error)))
        else const SizedBox.shrink(),
        ...children,
      ],
    )),
  );
}

class FormRow extends StatelessWidget {
  static const _spacing = 16.0;
  static const _titleMaxLines = 2;
  static const _titleMaxWidthFactor = 0.5;

  final String title;
  final TextStyle? titleStyle;
  final Widget? leading;
  final Widget? trailing;
  final bool invalid;
  final VoidCallback? onPressed;

  const FormRow({
    super.key,
    required this.title,
    this.titleStyle,
    this.leading,
    this.trailing,
    this.invalid = false,
    this.onPressed,
  });

  Widget _buildTitle(double maxWidth) {
    final text = TooltipLabel(
      title,
      style: titleStyle,
      maxLines: _titleMaxLines,
    );
    if (trailing == null) {
      return Flexible(child: text);
    }
    return ConstrainedBox(
      constraints: BoxConstraints(
        maxWidth: (maxWidth - _spacing) * _titleMaxWidthFactor,
      ),
      child: text,
    );
  }

  @override
  Widget build(BuildContext context) {
    return DecorationListItem(
      invalid: invalid,
      onPressed: onPressed,
      minVerticalPadding: trailing == null ? null : 0,
      leading: leading,
      title: LayoutBuilder(
        builder: (context, constraints) {
          return Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            spacing: _spacing,
            children: [
              _buildTitle(constraints.maxWidth),
              if (trailing != null)
                Flexible(
                  child: IconTheme(
                    data: IconThemeData(
                      size: 16.ap(context),
                      color: context.colorScheme.onSurface.opacity60,
                    ),
                    child: Container(
                      alignment: Alignment.centerRight,
                      height: MeasureScope.of(context).listRowHeight,
                      child: DefaultTextStyle.merge(
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        child: trailing!,
                      ),
                    ),
                  ),
                ),
            ],
          );
        },
      ),
    );
  }
}

/// A bare text field set against the end of a [FormRow], as iOS and macOS set
/// one in a grouped form; the row's focus ring is the only outline it gets.
class FormRowTextField extends StatelessWidget {
  static const _cursorWidth = 2.0;

  // RenderEditable's caret gap plus cursor, which it keeps clear past the text.
  static const _caretMargin = 1 + _cursorWidth;

  final TextEditingController? controller;
  final String? initialValue;
  final FocusNode? focusNode;
  final String? hintText;
  final TextInputType? keyboardType;
  final TextInputAction? textInputAction;
  final List<TextInputFormatter>? inputFormatters;
  final bool obscureText;
  final bool autofocus;
  final bool autocorrect;
  final bool enableSuggestions;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onFieldSubmitted;

  const FormRowTextField({
    super.key,
    this.controller,
    this.initialValue,
    this.focusNode,
    this.hintText,
    this.keyboardType,
    this.textInputAction,
    this.inputFormatters,
    this.obscureText = false,
    this.autofocus = false,
    this.autocorrect = true,
    this.enableSuggestions = true,
    this.onChanged,
    this.onFieldSubmitted,
  });

  @override
  Widget build(BuildContext context) {
    final towardEnd = switch (Directionality.of(context)) {
      TextDirection.ltr => 1.0,
      TextDirection.rtl => -1.0,
    };
    return InputDecorationTheme(
      data: InputDecorationTheme.of(
        context,
      ).copyWith(floatingLabelBehavior: FloatingLabelBehavior.never),
      child: Transform.translate(
        offset: Offset(_caretMargin * towardEnd, 0),
        child: _buildField(context),
      ),
    );
  }

  Widget _buildField(BuildContext context) {
    final hintText = this.hintText;
    return TextFormField(
      controller: controller,
      initialValue: initialValue,
      focusNode: focusNode,
      keyboardType: keyboardType,
      textInputAction: textInputAction,
      inputFormatters: inputFormatters,
      obscureText: obscureText,
      autofocus: autofocus,
      autocorrect: autocorrect,
      enableSuggestions: enableSuggestions,
      onChanged: onChanged,
      onFieldSubmitted: onFieldSubmitted,
      textAlign: TextAlign.end,
      cursorWidth: _cursorWidth,
      decoration: InputDecoration.collapsed(
        border: const NoInputBorder(),
        hintText: null,
        hint: hintText == null
            ? null
            : Padding(
                padding: const EdgeInsetsDirectional.only(end: _caretMargin),
                child: Text(
                  hintText,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  textAlign: TextAlign.end,
                  style: AppTextStyle.body
                      .of(context)
                      .copyWith(color: context.colorScheme.onSurfaceVariant),
                ),
              ),
      ),
    );
  }
}

/// A [FormRowTextField] for a secret, with the button that shows it.
class FormRowSecretTextField extends StatefulWidget {
  final TextEditingController controller;
  final FocusNode? focusNode;
  final String? hintText;
  final TextInputAction? textInputAction;
  final List<TextInputFormatter>? inputFormatters;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onFieldSubmitted;

  const FormRowSecretTextField({
    super.key,
    required this.controller,
    this.focusNode,
    this.hintText,
    this.textInputAction,
    this.inputFormatters,
    this.onChanged,
    this.onFieldSubmitted,
  });

  @override
  State<FormRowSecretTextField> createState() => _FormRowSecretTextFieldState();
}

class _FormRowSecretTextFieldState extends State<FormRowSecretTextField> {
  bool _obscure = true;

  @override
  Widget build(BuildContext context) {
    final appLocalizations = UiLocalizations.of(context);
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Flexible(
          child: FormRowTextField(
            controller: widget.controller,
            focusNode: widget.focusNode,
            hintText: widget.hintText,
            obscureText: _obscure,
            textInputAction: widget.textInputAction,
            autocorrect: false,
            enableSuggestions: false,
            inputFormatters: widget.inputFormatters,
            onChanged: widget.onChanged,
            onFieldSubmitted: widget.onFieldSubmitted,
          ),
        ),
        CommonMinIconButtonTheme(
          child: IconButton(
            tooltip: _obscure
                ? appLocalizations.showPassword
                : appLocalizations.hidePassword,
            icon: GlyphIcon(_obscure ? AppGlyphs.eye : AppGlyphs.eyeOff),
            onPressed: () {
              setState(() {
                _obscure = !_obscure;
              });
            },
          ),
        ),
      ],
    );
  }
}
