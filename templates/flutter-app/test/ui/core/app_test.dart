import 'package:app/ui/core/app.dart';
import 'package:app/ui/home/home_page.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('App starts on the home page', (tester) async {
    await tester.pumpWidget(const App());

    expect(find.byType(HomePage), findsOneWidget);
    expect(find.text(App.title), findsWidgets);
  });
}
