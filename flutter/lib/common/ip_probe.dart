class IpProbeResponse {
  const IpProbeResponse({required this.status, required this.body});
  final int status;
  final String body;
}

class IpProbeCancelled implements Exception {
  const IpProbeCancelled();
  @override String toString() => 'IP probe cancelled';
}

abstract interface class IpProbeTransport {
  bool get cancelled;
  Future<IpProbeResponse> get(String url, Duration timeout);
  Future<void> close();
}
