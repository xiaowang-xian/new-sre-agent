from django.urls import path
from . import views

urlpatterns = [
    path('health/', views.health),
    path('faults/', views.faults),
    path('stats/', views.stats),
    path('alerts/', views.alerts),
    path('nodes/', views.nodes),
    path('logs/', views.logs),
]
