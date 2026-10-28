// Adapted from FlClash v0.9.2 models/ip_quality.dart and enum/enum.dart (GPL-3.0).
enum IpType {
  residential('Residential'), mobile('Mobile'), business('Business'), hosting('Hosting');
  const IpType(this.label);
  final String label;
}

enum IpQualityLevel {
  good('Good'), normal('Normal'), risky('Risky');
  const IpQualityLevel(this.label);
  final String label;
}

enum IpQualitySource {
  identMe('ident.me'), ipApiCom('ip-api.com'), ipQuery('ipquery.io'),
  ipLocate('iplocate.io'), proxyCheck('proxycheck.io'), ipApiIs('ipapi.is');
  const IpQualitySource(this.label);
  final String label;
}

enum IpQualitySourceStatus { noType, timeout, rateLimited, failed, ipMismatch }

class IpQuality {
  const IpQuality({required this.ip, required this.source, required this.type,
    this.organization, this.asn, this.isProxy = false, this.isVpn = false,
    this.isTor = false, this.isAbuser = false});
  final String ip;
  final IpQualitySource source;
  final IpType type;
  final String? organization;
  final int? asn;
  final bool isProxy, isVpn, isTor, isAbuser;

  IpQualityLevel get level => isTor || isAbuser ? IpQualityLevel.risky
    : type == IpType.hosting ? IpQualityLevel.normal : IpQualityLevel.good;

  Map<String, Object?> toJson() => {'ip': ip, 'source': source.name, 'type': type.name,
    'organization': organization, 'asn': asn, 'isProxy': isProxy, 'isVpn': isVpn,
    'isTor': isTor, 'isAbuser': isAbuser};

  factory IpQuality.fromJson(Map<String, dynamic> json) => IpQuality(
    ip: json['ip'] as String,
    source: IpQualitySource.values.byName(json['source'] as String),
    type: IpType.values.byName(json['type'] as String),
    organization: json['organization'] as String?, asn: json['asn'] as int?,
    isProxy: json['isProxy'] as bool? ?? false, isVpn: json['isVpn'] as bool? ?? false,
    isTor: json['isTor'] as bool? ?? false, isAbuser: json['isAbuser'] as bool? ?? false,
  );

  Map<String, Object?> toDashboard(DateTime checkedAt) => {'organization': organization,
    'asn': asn == null ? null : 'AS$asn', 'type': type.label, 'quality': level.label,
    'source': source.label, 'qualityCheckedAt': checkedAt.millisecondsSinceEpoch,
    'isProxy': isProxy, 'isVpn': isVpn, 'isTor': isTor, 'isAbuser': isAbuser};

  @override bool operator ==(Object other) => other is IpQuality && ip == other.ip
    && source == other.source && type == other.type && organization == other.organization
    && asn == other.asn && isProxy == other.isProxy && isVpn == other.isVpn
    && isTor == other.isTor && isAbuser == other.isAbuser;
  @override int get hashCode => Object.hash(ip, source, type, organization, asn, isProxy, isVpn, isTor, isAbuser);
}

class IpQualityRecord {
  const IpQualityRecord(this.quality, this.checkedAt);
  final IpQuality quality;
  final DateTime checkedAt;
  Map<String, Object?> toJson() => {'quality': quality.toJson(), 'checkedAt': checkedAt.millisecondsSinceEpoch};
  factory IpQualityRecord.fromJson(Map<String, dynamic> json) => IpQualityRecord(
    IpQuality.fromJson(Map<String, dynamic>.from(json['quality'] as Map)),
    DateTime.fromMillisecondsSinceEpoch(json['checkedAt'] as int));
}
