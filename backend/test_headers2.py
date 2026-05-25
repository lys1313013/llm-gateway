from werkzeug.datastructures import Headers
h = Headers()
h.add("X-Test", "1")
h.add("X-Test", "2")
print("list:", list(h.items()))
