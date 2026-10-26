import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'ui/l10n.dart';

const _zh = <String, String>{
  'Dashboard': '仪表盘', 'Proxies': '代理', 'Profiles': '配置', 'Tools': '工具',
  'Connections': '连接', 'Logs': '日志', 'Settings': '设置', 'Status': '状态',
  'Running': '运行中', 'Not running': '未运行', 'Starting': '启动中', 'Stopping': '停止中',
  'Start': '启动', 'Stop': '停止', 'Restart': '重启', 'Reload': '重新加载',
  'Network speed': '网络速度', 'Upload': '上传', 'Download': '下载', 'Total': '累计',
  'Outbound mode': '出站模式', 'Rule': '规则', 'Global': '全局', 'Direct': '直连',
  'Proxy groups': '代理组', 'Memory': '内存', 'Goroutines': '协程',
  'Current profile': '当前配置', 'No profile': '未选择配置', 'Add profile': '添加配置',
  'Import': '导入', 'URL': '链接', 'File': '文件', 'Clipboard': '剪贴板', 'Scan QR code': '扫描二维码',
  'Name': '名称', 'Save': '保存', 'Cancel': '取消', 'Back': '返回', 'Close': '关闭',
  'Delete': '删除', 'Edit': '编辑', 'Update': '更新', 'Update all': '全部更新',
  'Share': '分享', 'Copy': '复制', 'Copied': '已复制', 'Export': '导出',
  'Local': '本地', 'Remote': '远程', 'Last updated': '上次更新',
  'Auto update': '自动更新', 'Update interval (minutes)': '更新间隔（分钟）',
  'Configuration': '配置内容', 'Format': '格式化', 'Validate': '校验',
  'Configuration is valid': '配置校验通过', 'Unsaved changes': '有未保存的修改',
  'Discard changes?': '放弃未保存的修改？', 'Discard': '放弃',
  'Delete this profile?': '删除此配置？', 'This cannot be undone.': '此操作无法撤销。',
  'Search': '搜索', 'Clear search': '清空搜索', 'No results': '没有搜索结果',
  'No search results': '没有搜索结果', 'More': '更多', 'Reset': '重置',
  'Show less': '收起', 'Show more': '展开', 'Show password': '显示密码', 'Hide password': '隐藏密码',
  'Sort': '排序', 'Submit': '提交', 'Default': '默认', 'Latency': '延迟',
  'Test latency': '测试延迟', 'Test all': '全部测速', 'Testing': '测速中',
  'Unreachable': '不可达', 'Not tested': '未测试', 'Selected': '已选择', 'Select': '选择',
  'Automatic selection': '内核自动选择', 'Close connections after switching': '切换后关闭连接',
  'Start the service to view proxy groups': '启动服务后查看代理组',
  'No proxy groups in this configuration': '当前配置没有代理组',
  'View': '查看', 'Details': '详情', 'Active': '活动', 'Closed': '已关闭', 'All': '全部',
  'Close all connections': '关闭所有连接', 'Close this connection': '关闭此连接',
  'Close all active connections?': '关闭所有活动连接？',
  'Destination': '目标地址', 'Source': '来源地址', 'Protocol': '协议', 'Network': '网络',
  'Rule matched': '匹配规则', 'Outbound': '出站', 'Inbound': '入站', 'Chain': '代理链',
  'Process': '进程', 'Created': '创建时间', 'No connections': '暂无连接',
  'Pause': '暂停', 'Resume': '继续', 'Clear': '清空', 'No logs': '暂无日志',
  'Level': '级别', 'Error': '错误', 'Retry': '重试', 'Done': '完成', 'OK': '确定',
  'Appearance': '外观', 'Theme': '主题', 'System': '跟随系统', 'Light': '浅色', 'Dark': '深色',
  'Language': '语言', 'Accent color': '主题色',
  'Blue': '蓝色', 'Purple': '紫色', 'Pink': '粉色', 'Red': '红色',
  'Orange': '橙色', 'Yellow': '黄色', 'Green': '绿色', 'Gray': '灰色', 'Color style': '配色风格',
  'Vivid': '鲜明', 'Tinted': '染色', 'Soft': '柔和', 'Monochrome': '单色',
  'Floating navigation bar': '悬浮导航栏', 'Font scale': '字体缩放',
  'Dashboard cards': '仪表盘卡片', 'Reorder': '调整顺序', 'Columns': '列数',
  'Layout': '布局', 'Tabs': '标签页', 'List': '列表', 'Compact': '紧凑',
  'Access control': '访问控制', 'Per-app proxy': '分应用代理', 'Mode': '模式',
  'Bypass selected apps': '绕过选中的应用', 'Proxy selected apps': '仅代理选中的应用',
  'Show system apps': '显示系统应用', 'Selected apps': '已选应用', 'Application query': '应用查询',
  'Grant Shizuku permission': '授予 Shizuku 权限', 'Managed app list': '托管应用列表',
  'Disable managed app list to edit selection': '关闭托管应用列表后可编辑选择',
  'Service': '服务', 'System HTTP proxy': '系统 HTTP 代理', 'Allow VPN bypass': '允许绕过 VPN',
  'Auto redirect': '自动重定向', 'Dynamic notification': '动态通知', 'Battery optimization': '电池优化',
  'Unrestricted': '无限制', 'Allow background operation': '允许后台运行',
  'Apply service changes': '应用服务设置', 'Restart required': '需要重启服务',
  'Apply': '应用', 'Later': '稍后', 'Core': '内核', 'Core version': '内核版本',
  'Disable deprecation warnings': '关闭弃用警告', 'Memory limit': '内存限制',
  'Enable memory protection': '启用内存保护', 'Disable core memory killer': '禁用内核内存终止机制',
  'Power reports': '电量报告', 'Remote control': '远程控制', 'Add server': '添加服务器',
  'Address': '地址', 'Secret': '密钥', 'Connect': '连接', 'Disconnect': '断开',
  'Disconnect remote session to edit local profiles': '断开远程会话后可编辑本地配置',
  'Backup and restore': '备份与恢复', 'Create backup': '创建备份', 'Restore backup': '恢复备份',
  'About': '关于', 'Source code': '源代码', 'Open-source licenses': '开源许可',
  'FlClash interface': 'FlClash 界面', 'Version': '版本', 'Network diagnostics': '网络诊断',
  'Network quality': '网络质量', 'STUN test': 'STUN 测试',
  'No endpoints': '没有端点', 'Endpoints': '端点', 'Peers': '设备',
  'Exit node': '出口节点', 'Logout': '退出登录', 'Terminal': '终端', 'Inbox': '收件箱',
  'Send file': '发送文件', 'Receive files': '接收文件',
  'sing-box JSON URL': 'sing-box JSON 配置链接',
  'This URL must serve a sing-box JSON configuration.': '此链接需要返回 sing-box JSON 配置。',
  'Empty clipboard': '剪贴板为空', 'Required': '必填', 'At least 15 minutes': '至少 15 分钟',
  'No profiles yet': '还没有配置', 'Import a sing-box JSON configuration to get started.': '导入 sing-box JSON 配置即可开始使用。',
  'Configuration changes will replace the remote content until its next update.': '修改将保留到远程配置下次更新。',
  'New local profile': '新建本地配置', 'Import from URL': '从链接导入',
  'Open configuration': '打开配置内容', 'QR code': '二维码', 'No remote servers': '暂无远程服务器',
  "Disconnected": "已断开",
  "Username": "用户名",
  "Password": "密码",
  "Verification code": "验证码",
  "Sign in": "登录",
  "Sign out": "退出登录",
  "None": "无",
  "Online": "在线",
  "Offline": "离线",
  "OS": "系统",
  "Path": "路径",
  "Authentication": "身份验证",
  "Open sign-in page": "打开登录页面",
  "Endpoint disconnected": "端点已断开",
  "Peer disconnected": "设备已断开",
  "No USB devices": "没有 USB 设备",
  "Attach USB device": "连接 USB 设备",
  "Shared devices": "已共享设备",
  "Detach": "断开设备",
  "Transfers": "传输任务",
  "No transfers": "暂无传输任务",
  "Received files": "收到的文件",
  "No received files": "没有收到的文件",
  "Send with Taildrop": "通过 Taildrop 发送",
  "Service not started": "服务尚未启动",
  "No recipients": "没有可接收的设备",
  "Paste": "粘贴",
  "Reports": "报告",
  "Crash reports": "崩溃报告",
  "Memory reports": "内存报告",
  "No reports": "暂无报告",
  "Delete all reports?": "删除所有报告？",
  "Include configuration": "包含配置文件",
  "Include logs": "包含日志",
  "Encrypt for sing-box maintainers": "加密给 sing-box 维护者",
  "Export archive": "导出压缩包",
  "Refresh": "刷新",
  "Serial test": "串行测试",
  "Duration": "时长",
  "This test transfers data on a metered network. Continue?": "当前网络按流量计费，测试会消耗数据流量。继续？",
  "Import profiles and remote servers, and restore settings from this backup?": "导入备份中的配置和远程服务器，并恢复设置？",
  'Loading': '加载中', 'Start failed': '启动失败',
};

extension BoxLocalization on BuildContext {
  String tr(String value) => Localizations.localeOf(this).languageCode == 'zh' ? _zh[value] ?? value : value;
}

class BoxUiLocalizations extends UiLocalizations {
  BoxUiLocalizations(this.locale);
  final Locale locale;
  String _t(String value) => locale.languageCode == 'zh' ? _zh[value] ?? value : value;
  @override String get back => _t('Back');
  @override String get cancel => _t('Cancel');
  @override String get clearSearch => _t('Clear search');
  @override String get close => _t('Close');
  @override String get hidePassword => _t('Hide password');
  @override String get more => _t('More');
  @override String get noSearchResults => _t('No search results');
  @override String get reset => _t('Reset');
  @override String get search => _t('Search');
  @override String get showLess => _t('Show less');
  @override String get showMore => _t('Show more');
  @override String get showPassword => _t('Show password');
  @override String get sort => _t('Sort');
  @override String get submit => _t('Submit');
}

class BoxUiDelegate extends LocalizationsDelegate<UiLocalizations> {
  const BoxUiDelegate();
  @override bool isSupported(Locale locale) => true;
  @override Future<UiLocalizations> load(Locale locale) => SynchronousFuture(BoxUiLocalizations(locale));
  @override bool shouldReload(BoxUiDelegate old) => false;
}
