# Sourced only by the isolated-kind lifecycle test.
echo 'Checking multi-site routing and validation'
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=json -p '[{"op":"add","path":"/spec/sites/-","value":{"id":"s2","hosts":["two.example.com","alias.example.com"]}}]'
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=jsonpath='{.status.observedGeneration}'="$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.metadata.generation}')" --timeout=120s
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=condition=Ready --timeout=240s
for pair in portal.example.com:s1 two.example.com:s2 alias.example.com:s2; do
    response=$(kubectl -n bitrix-web-test exec deployment/mysql-web -c nginx -- wget -qO- --header="Host: ${pair%:*}" --header='Bitrix-Site-ID: spoof' http://mysql-web/site.php)
    test "$response" = "${pair#*:}"
done
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=json -p '[{"op":"replace","path":"/spec/sites/1/id","value":"absent"}]'
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=jsonpath='{.status.conditions[?(@.type=="SitesValidated")].reason}'=SiteNotFound --timeout=240s
test "$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = False
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=json -p '[{"op":"remove","path":"/spec/sites/1"}]'
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=jsonpath='{.status.observedGeneration}'="$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.metadata.generation}')" --timeout=120s
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=condition=Ready --timeout=240s
# Stale Jobs and their Pods must be garbage-collected after each generation change.
test "$(kubectl -n bitrix-web-test get jobs -l app.kubernetes.io/name=bitrix-sites -o name | wc -l)" -eq 2
