client.test("two products", function () {
    client.assert(response.body.items.length === 2, "items");
    client.assert(jsonPath(response.body, "$.items[1].name") === "Mug", "second is Mug");
});
client.global.set("firstProduct", response.body.items[0].name);
