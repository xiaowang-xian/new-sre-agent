import requests
from django.conf import settings
from rest_framework.decorators import api_view
from rest_framework.response import Response
from elasticsearch import Elasticsearch
from kubernetes import client, config
from .models import FaultRecord
from .serializers import FaultRecordSerializer


def get_k8s_core():
    try:
        config.load_incluster_config()
    except Exception:
        config.load_kube_config()
    return client.CoreV1Api()


@api_view(['GET'])
def health(request):
    return Response({'status': 'ok'})


@api_view(['GET'])
def faults(request):
    qs = FaultRecord.objects.all()[:100]
    return Response(FaultRecordSerializer(qs, many=True).data)


@api_view(['GET'])
def stats(request):
    total = FaultRecord.objects.count()
    success = FaultRecord.objects.filter(status='success').count()
    failed = FaultRecord.objects.filter(status='failed').count()
    running = FaultRecord.objects.filter(status='running').count()
    return Response({'total': total, 'success': success, 'failed': failed, 'running': running})


@api_view(['GET'])
def alerts(request):
    url = settings.PROMETHEUS_URL.rstrip('/') + '/api/v1/alerts'
    try:
        r = requests.get(url, timeout=5)
        return Response(r.json())
    except Exception as e:
        return Response({'status': 'error', 'message': str(e)}, status=500)


@api_view(['GET'])
def nodes(request):
    try:
        v1 = get_k8s_core()
        items = []
        for n in v1.list_node().items:
            ready = 'Unknown'
            for c in n.status.conditions:
                if c.type == 'Ready':
                    ready = c.status
            items.append({'name': n.metadata.name, 'ready': ready, 'unschedulable': n.spec.unschedulable or False})
        return Response({'items': items})
    except Exception as e:
        return Response({'status': 'error', 'message': str(e)}, status=500)


@api_view(['GET'])
def logs(request):
    keyword = request.GET.get('q', '')
    es = Elasticsearch(settings.ELASTICSEARCH_URL)
    query = {'match_all': {}} if not keyword else {'match': {'message': keyword}}
    try:
        res = es.search(index='sre-agent-logs', query=query, size=50, sort=[{'@timestamp': {'order': 'desc'}}])
        hits = [h['_source'] for h in res['hits']['hits']]
        return Response({'items': hits})
    except Exception as e:
        return Response({'status': 'error', 'message': str(e), 'items': []})
